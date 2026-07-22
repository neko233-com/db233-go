package db233

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const secureAtomicTempPattern = ".db233-private-export-*.tmp"

// secureAtomicFileOps keeps failure injection local to tests. Production calls
// always use defaultSecureAtomicFileOps, so concurrent exports do not mutate
// package-level hooks.
type secureAtomicFileOps struct {
	createTemp    func(string, string) (*os.File, error)
	chmodTemp     func(*os.File, os.FileMode) error
	hardenFile    func(string) error
	writeAll      func(*os.File, []byte) error
	syncTemp      func(*os.File) error
	closeTemp     func(*os.File) error
	removeTemp    func(string) error
	replace       func(string, string) error
	syncDirectory func(string) error
}

func defaultSecureAtomicFileOps() secureAtomicFileOps {
	return secureAtomicFileOps{
		createTemp: os.CreateTemp,
		chmodTemp: func(file *os.File, mode os.FileMode) error {
			return file.Chmod(mode)
		},
		hardenFile: hardenRecoveryFilePermissions,
		writeAll:   writeSecureAtomicBytes,
		syncTemp: func(file *os.File) error {
			return file.Sync()
		},
		closeTemp: func(file *os.File) error {
			return file.Close()
		},
		removeTemp:    os.Remove,
		replace:       replaceSecureAtomicFile,
		syncDirectory: syncRecoveryDirectory,
	}
}

// writeSecureAtomicFile writes private monitoring artifacts without exposing a
// partially written destination. Replacement is atomic because the temporary
// file is created in the destination directory.
func writeSecureAtomicFile(path string, data []byte) error {
	return writeSecureAtomicFileWithOps(path, data, defaultSecureAtomicFileOps())
}

func marshalSecureExportJSON(value any) (data []byte, resultErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			data = nil
			resultErr = fmt.Errorf("序列化安全导出数据发生 panic: %s", safeValueForLog(recovered))
		}
	}()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func writeSecureAtomicFileWithOps(path string, data []byte, ops secureAtomicFileOps) (resultErr error) {
	if path == "" {
		return errors.New("安全导出文件路径不能为空")
	}
	dir := filepath.Dir(path)
	if err := ensureSecureAtomicDirectory(dir); err != nil {
		return fmt.Errorf("准备安全导出目录: %w", err)
	}
	if err := hardenSecureAtomicDestinationIfExists(path, ops.hardenFile); err != nil {
		return fmt.Errorf("检查安全导出目标: %w", err)
	}

	temp, err := ops.createTemp(dir, secureAtomicTempPattern)
	if err != nil {
		return fmt.Errorf("创建安全导出临时文件: %w", err)
	}
	tempPath := temp.Name()
	tempClosed := false
	tempReplaced := false
	defer func() {
		if !tempClosed {
			if closeErr := ops.closeTemp(temp); closeErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("关闭安全导出临时文件: %w", closeErr))
			}
		}
		if !tempReplaced {
			if removeErr := ops.removeTemp(tempPath); removeErr != nil && !os.IsNotExist(removeErr) {
				resultErr = errors.Join(resultErr, fmt.Errorf("清理安全导出临时文件: %w", removeErr))
			}
		}
	}()

	// CreateTemp is 0600 on Unix. Explicit mode plus protected Windows DACL keep
	// the invariant visible and fail closed if either hardening step fails.
	if err := ops.chmodTemp(temp, recoveryFileMode); err != nil {
		return fmt.Errorf("设置安全导出临时文件权限: %w", err)
	}
	if err := ops.hardenFile(tempPath); err != nil {
		return fmt.Errorf("收紧安全导出临时文件权限: %w", err)
	}
	if err := ops.writeAll(temp, data); err != nil {
		return fmt.Errorf("完整写入安全导出临时文件: %w", err)
	}
	if err := ops.syncTemp(temp); err != nil {
		return fmt.Errorf("同步安全导出临时文件: %w", err)
	}
	tempClosed = true
	if err := ops.closeTemp(temp); err != nil {
		return fmt.Errorf("关闭安全导出临时文件: %w", err)
	}
	if err := ops.replace(tempPath, path); err != nil {
		return fmt.Errorf("原子替换安全导出文件: %w", err)
	}
	tempReplaced = true
	if err := ops.syncDirectory(dir); err != nil {
		return fmt.Errorf("同步安全导出目录: %w", err)
	}
	return nil
}

func writeSecureAtomicBytes(file *os.File, data []byte) error {
	for len(data) > 0 {
		written, err := file.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 {
			return errors.New("安全导出写入未取得进展")
		}
		data = data[written:]
	}
	return nil
}

func hardenSecureAtomicDestinationIfExists(path string, harden func(string) error) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("安全导出目标不能是符号链接")
	}
	if !info.Mode().IsRegular() {
		return errors.New("安全导出目标必须是普通文件")
	}
	if err := harden(path); err != nil {
		return fmt.Errorf("收紧现有安全导出文件权限: %w", err)
	}
	return nil
}

// ensureSecureAtomicDirectory creates only missing directories. Existing
// directories are validated but never chmod'ed, avoiding permission changes to
// a caller-owned working directory. Every directory created here is private.
func ensureSecureAtomicDirectory(dir string) error {
	dir = filepath.Clean(dir)
	missing := make([]string, 0, 2)
	current := dir
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return errors.New("安全导出目录不能是符号链接")
			}
			if !info.IsDir() {
				return errors.New("安全导出父路径不是目录")
			}
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			return err
		}
		current = parent
	}

	for index := len(missing) - 1; index >= 0; index-- {
		path := missing[index]
		if err := os.Mkdir(path, recoveryDirectoryMode); err != nil && !os.IsExist(err) {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("新建安全导出路径不是可信目录")
		}
		if err := hardenRecoveryDirectoryPermissions(path); err != nil {
			return fmt.Errorf("收紧新建安全导出目录权限: %w", err)
		}
	}
	return nil
}
