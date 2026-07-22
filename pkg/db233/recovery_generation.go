package db233

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ErrDatabaseGenerationBlocked 表示恢复队列无法确认属于当前数据库代次。
// 此状态必须人工修复或成功轮换代次，禁止继续写入/回放。
var ErrDatabaseGenerationBlocked = errors.New("数据库代次保护已阻断恢复队列")

// ErrDatabaseGenerationChanged 表示对象属于旧数据库代次，不能继续落库。
var ErrDatabaseGenerationChanged = errors.New("数据库代次已变化")

const (
	recoveryDirectoryMode     = 0700
	recoveryFileMode          = 0600
	quarantineInspectionLimit = 256
	quarantineNameAttempts    = 16
)

type recoveryGenerationManifest struct {
	FormatVersion      int       `json:"formatVersion"`
	DatabaseGeneration string    `json:"databaseGeneration"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

func prepareRecoveryGeneration(
	dir string,
	manifestName string,
	dataPaths []string,
	component string,
	generation string,
	forceRotate bool,
) error {
	if generation == "" {
		return nil // 兼容模式；生产环境应始终配置 generation。
	}
	if err := ensurePrivateRecoveryDirectory(dir); err != nil {
		return fmt.Errorf("创建恢复目录: %w", err)
	}
	for _, path := range dataPaths {
		if err := validateRecoveryPath(dir, path); err != nil {
			return fmt.Errorf("检查 %s 恢复路径: %w", component, err)
		}
		if err := ensurePrivateRecoveryFileIfExists(path); err != nil {
			return fmt.Errorf("收紧 %s 恢复文件权限: %w", component, err)
		}
	}

	manifestPath := filepath.Join(dir, manifestName)
	manifest, manifestErr := readRecoveryGenerationManifest(manifestPath)
	matched := manifestErr == nil && manifest.DatabaseGeneration == generation
	if matched && !forceRotate {
		return nil
	}

	manifestMissing := os.IsNotExist(manifestErr)
	manifestInvalid := manifestErr != nil && !manifestMissing
	dataExists, dataCheckErr := anyRecoveryDataExists(dataPaths)
	if dataCheckErr != nil {
		return fmt.Errorf("检查 %s 恢复数据: %w", component, dataCheckErr)
	}
	unsafeMissingManifest := manifestMissing && dataExists
	if manifestInvalid || unsafeMissingManifest {
		cause := manifestErr
		if unsafeMissingManifest {
			cause = errors.New("generation manifest 缺失但恢复数据仍存在")
		}
		LogWarn("%s generation 元数据不可安全确认，将隔离并保持阻断: %v", component, cause)
		var quarantineErrors []error
		for _, path := range dataPaths {
			if err := quarantineRecoveryFile(dir, path, component); err != nil {
				quarantineErrors = append(quarantineErrors, fmt.Errorf("隔离恢复数据 %s: %w", filepath.Base(path), err))
			}
		}
		if !manifestMissing {
			if err := quarantineRecoveryFile(dir, manifestPath, component+"-manifest"); err != nil {
				quarantineErrors = append(quarantineErrors, fmt.Errorf("隔离 generation manifest: %w", err))
			}
		}
		return errors.Join(
			fmt.Errorf("%w: %s generation 无法验证: %v", ErrDatabaseGenerationBlocked, component, cause),
			errors.Join(quarantineErrors...),
		)
	}

	shouldQuarantineData := forceRotate || !matched
	if shouldQuarantineData {
		for _, path := range dataPaths {
			if err := quarantineRecoveryFile(dir, path, component); err != nil {
				return fmt.Errorf("隔离 %s 恢复数据: %w", component, err)
			}
		}
	}
	// 已验证的旧 manifest 不是恢复数据，直接由原子写覆盖；这样正常代次轮换
	// 不会无界累积无价值 manifest。损坏 manifest 已在上面的阻断分支保全隔离。
	manifest = recoveryGenerationManifest{
		FormatVersion:      1,
		DatabaseGeneration: generation,
		UpdatedAt:          time.Now().UTC(),
	}
	if err := writeJSONAtomic(manifestPath, manifest, recoveryFileMode); err != nil {
		return fmt.Errorf("写入 %s generation manifest: %w", component, err)
	}
	return nil
}

func readRecoveryGenerationManifest(path string) (recoveryGenerationManifest, error) {
	var manifest recoveryGenerationManifest
	if err := ensurePrivateRecoveryFileIfExists(path); err != nil {
		return manifest, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	if manifest.FormatVersion != 1 || manifest.DatabaseGeneration == "" {
		return manifest, fmt.Errorf("不支持的 manifest: formatVersion=%d generation=%s", manifest.FormatVersion, safeValueForLog(manifest.DatabaseGeneration))
	}
	return manifest, nil
}

func quarantineRecoveryFile(dir, path, component string) error {
	if err := ensurePrivateRecoveryDirectory(dir); err != nil {
		return err
	}
	if err := validateRecoveryPath(dir, path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("恢复文件不能是符号链接: %s", safeValueForLog(path))
	}
	if info.IsDir() {
		return fmt.Errorf("恢复文件是目录: %s", safeValueForLog(path))
	}
	if err := hardenRecoveryFilePermissions(path); err != nil {
		return fmt.Errorf("收紧恢复文件权限: %w", err)
	}
	quarantineDir := filepath.Join(dir, "quarantine")
	if err := ensurePrivateRecoveryDirectory(quarantineDir); err != nil {
		return err
	}
	if count, err := countDirectoryEntriesBounded(quarantineDir, quarantineInspectionLimit+1); err != nil {
		return fmt.Errorf("检查隔离目录: %w", err)
	} else if count > quarantineInspectionLimit {
		// 不自动删除唯一恢复数据；仅限制每次检查成本并提示运维归档。
		LogWarn("恢复隔离目录文件较多(>%d)，请人工归档: %s", quarantineInspectionLimit, safeValueForLog(quarantineDir))
	}

	nonce, err := recoveryQuarantineNonce()
	if err != nil {
		return fmt.Errorf("生成恢复隔离标识: %w", err)
	}
	component = sanitizeQuarantineName(component, 48)
	base := sanitizeQuarantineName(filepath.Base(path), 96)
	for attempt := 0; attempt < quarantineNameAttempts; attempt++ {
		name := fmt.Sprintf("%s-%d-%s-%02d-%s", component, time.Now().UTC().UnixNano(), nonce, attempt, base)
		destination := filepath.Join(quarantineDir, name)
		if _, statErr := os.Lstat(destination); statErr == nil {
			continue
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		if err := os.Rename(path, destination); err != nil {
			return err
		}
		if err := hardenRecoveryFilePermissions(destination); err != nil {
			return fmt.Errorf("收紧隔离文件权限: %w", err)
		}
		return syncRecoveryDirectories(filepath.Dir(path), quarantineDir)
	}
	return fmt.Errorf("生成隔离文件名冲突次数超过 %d", quarantineNameAttempts)
}

func writeJSONAtomic(path string, value any, mode os.FileMode) (resultErr error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := ensurePrivateRecoveryDirectory(dir); err != nil {
		return err
	}
	if err := ensurePrivateRecoveryFileIfExists(path); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".db233-generation-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmpClosed := false
	tmpRenamed := false
	defer func() {
		if !tmpClosed {
			resultErr = errors.Join(resultErr, tmp.Close())
		}
		if !tmpRenamed {
			if removeErr := os.Remove(tmpPath); removeErr != nil && !os.IsNotExist(removeErr) {
				resultErr = errors.Join(resultErr, removeErr)
			}
		}
	}()
	// 恢复文件可能包含 SQL 参数、实体快照和业务主键，始终使用私密权限。
	_ = mode // 保留内部兼容参数；安全策略固定为 0600。
	if err := tmp.Chmod(recoveryFileMode); err != nil {
		return err
	}
	if err := hardenRecoveryFilePermissions(tmpPath); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		tmpClosed = true
		return err
	}
	tmpClosed = true
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	tmpRenamed = true
	if err := hardenRecoveryFilePermissions(path); err != nil {
		return err
	}
	return syncRecoveryDirectory(dir)
}

func ensurePrivateRecoveryDirectory(dir string) error {
	if dir == "" {
		return errors.New("恢复目录不能为空")
	}
	if err := os.MkdirAll(dir, recoveryDirectoryMode); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("恢复目录不能是符号链接: %s", safeValueForLog(dir))
	}
	if !info.IsDir() {
		return fmt.Errorf("恢复路径不是目录: %s", safeValueForLog(dir))
	}
	if err := hardenRecoveryDirectoryPermissions(dir); err != nil {
		return fmt.Errorf("收紧恢复目录权限: %w", err)
	}
	return nil
}

func ensurePrivateRecoveryFileIfExists(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("恢复路径不能是符号链接: %s", safeValueForLog(path))
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("恢复路径不是普通文件: %s", safeValueForLog(path))
	}
	if err := hardenRecoveryFilePermissions(path); err != nil {
		return fmt.Errorf("收紧恢复文件权限: %w", err)
	}
	return nil
}

func validateRecoveryPath(dir, path string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(absDir, absPath)
	if err != nil {
		return err
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("恢复文件必须位于恢复目录内: dir=%s path=%s", safeValueForLog(dir), safeValueForLog(path))
	}
	return nil
}

func anyRecoveryDataExists(paths []string) (bool, error) {
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return false, fmt.Errorf("恢复路径不能是符号链接: %s", safeValueForLog(path))
			}
			if !info.Mode().IsRegular() {
				return false, fmt.Errorf("恢复路径不是普通文件: %s", safeValueForLog(path))
			}
			return true, nil
		}
		if !os.IsNotExist(err) {
			return false, err
		}
	}
	return false, nil
}

func removeRecoveryFile(path string) error {
	if err := ensurePrivateRecoveryFileIfExists(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return syncRecoveryDirectory(filepath.Dir(path))
}

func syncRecoveryDirectories(dirs ...string) error {
	seen := make(map[string]struct{}, len(dirs))
	var syncErrors []error
	for _, dir := range dirs {
		clean := filepath.Clean(dir)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		if err := syncRecoveryDirectory(clean); err != nil {
			syncErrors = append(syncErrors, err)
		}
	}
	return errors.Join(syncErrors...)
}

func syncRecoveryDirectory(dir string) (resultErr error) {
	// Windows 标准库不能稳定 fsync 目录；Rename/Remove 仍使用系统原子操作，
	// Unix 则同步父目录以持久化目录项。
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, f.Close()) }()
	return f.Sync()
}

func countDirectoryEntriesBounded(dir string, limit int) (count int, resultErr error) {
	f, err := os.Open(dir)
	if err != nil {
		return 0, err
	}
	defer func() { resultErr = errors.Join(resultErr, f.Close()) }()
	entries, err := f.ReadDir(limit)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	return len(entries), nil
}

func recoveryQuarantineNonce() (string, error) {
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(entropy[:]), nil
}

func sanitizeQuarantineName(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return "recovery"
	}
	var builder strings.Builder
	for _, r := range value {
		if builder.Len() >= maxRunes {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	name := strings.Trim(builder.String(), ". ")
	if name == "" {
		return "recovery"
	}
	return name
}
