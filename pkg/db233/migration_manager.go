package db233

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

/**
 * MigrationManager - 数据迁移管理器
 *
 * 管理数据库模式迁移，支持版本控制和回滚
 *
 * @author SolarisNeko
 * @since 2025-12-29
 */
type MigrationManager struct {
	db            *Db
	tableName     string
	migrationsDir string
}

/**
 * Migration - 迁移记录
 */
type Migration struct {
	Version   int64
	Name      string
	UpSQL     string
	DownSQL   string
	AppliedAt *time.Time
}

/**
 * 创建迁移管理器
 */
func NewMigrationManager(db *Db, migrationsDir string) *MigrationManager {
	return &MigrationManager{
		db:            db,
		tableName:     "schema_migrations",
		migrationsDir: migrationsDir,
	}
}

/**
 * 初始化迁移表
 */
func (mm *MigrationManager) Init() error {
	createTableSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			version BIGINT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`, mm.tableName)

	_, err := mm.db.DataSource.Exec(createTableSQL)
	if err != nil {
		return NewQueryExceptionWithCause(err, "创建迁移表失败")
	}

	LogInfo("迁移表已初始化: %s", mm.tableName)
	return nil
}

/**
 * 创建新的迁移文件
 */
func (mm *MigrationManager) CreateMigration(name string) error {
	version := time.Now().Unix()
	upFile := filepath.Join(mm.migrationsDir, fmt.Sprintf("%d_%s.up.sql", version, name))
	downFile := filepath.Join(mm.migrationsDir, fmt.Sprintf("%d_%s.down.sql", version, name))

	// 创建上迁文件
	upContent := fmt.Sprintf("-- Migration: %s\n-- Version: %d\n-- Created: %s\n\n-- Add your up migration SQL here\n\n",
		name, version, time.Now().Format(time.RFC3339))

	err := ioutil.WriteFile(upFile, []byte(upContent), 0644)
	if err != nil {
		return NewConfigurationExceptionWithCause(err, "创建上迁文件失败")
	}

	// 创建下迁文件
	downContent := fmt.Sprintf("-- Migration: %s\n-- Version: %d\n-- Created: %s\n\n-- Add your down migration SQL here\n\n",
		name, version, time.Now().Format(time.RFC3339))

	err = ioutil.WriteFile(downFile, []byte(downContent), 0644)
	if err != nil {
		return NewConfigurationExceptionWithCause(err, "创建下迁文件失败")
	}

	LogInfo("迁移文件已创建: %s", name)
	return nil
}

/**
 * 执行上迁
 */
func (mm *MigrationManager) Up(steps int) error {
	// 获取待应用的迁移
	pendingMigrations, err := mm.getPendingMigrations()
	if err != nil {
		return err
	}

	if len(pendingMigrations) == 0 {
		LogInfo("没有待应用的迁移")
		return nil
	}

	// 限制步骤数
	if steps > 0 && steps < len(pendingMigrations) {
		pendingMigrations = pendingMigrations[:steps]
	}

	// 应用迁移
	for _, migration := range pendingMigrations {
		err := mm.applyMigration(migration, true)
		if err != nil {
			return fmt.Errorf("应用迁移失败 %d_%s: %w", migration.Version, migration.Name, err)
		}
	}

	LogInfo("成功应用 %d 个迁移", len(pendingMigrations))
	return nil
}

/**
 * 执行下迁
 */
func (mm *MigrationManager) Down(steps int) error {
	// 获取已应用的迁移
	appliedMigrations, err := mm.getAppliedMigrations()
	if err != nil {
		return err
	}

	if len(appliedMigrations) == 0 {
		LogInfo("没有已应用的迁移")
		return nil
	}

	// 反转顺序（最新的先回滚）
	for i := len(appliedMigrations) - 1; i >= 0; i-- {
		appliedMigrations[i] = appliedMigrations[len(appliedMigrations)-1-i]
	}

	// 限制步骤数
	if steps > 0 && steps < len(appliedMigrations) {
		appliedMigrations = appliedMigrations[:steps]
	}

	// 回滚迁移
	for _, migration := range appliedMigrations {
		err := mm.applyMigration(migration, false)
		if err != nil {
			return fmt.Errorf("回滚迁移失败 %d_%s: %w", migration.Version, migration.Name, err)
		}
	}

	LogInfo("成功回滚 %d 个迁移", len(appliedMigrations))
	return nil
}

/**
 * 迁移到指定版本
 */
func (mm *MigrationManager) MigrateToVersion(targetVersion int64) error {
	currentVersion, err := mm.getCurrentVersion()
	if err != nil {
		return err
	}

	if currentVersion == targetVersion {
		LogInfo("当前已是目标版本: %d", targetVersion)
		return nil
	}

	if currentVersion < targetVersion {
		// 上迁到目标版本
		return mm.upToVersion(targetVersion)
	} else {
		// 下迁到目标版本
		return mm.downToVersion(targetVersion)
	}
}

/**
 * 获取当前版本
 */
func (mm *MigrationManager) GetCurrentVersion() (int64, error) {
	return mm.getCurrentVersion()
}

/**
 * 获取迁移状态
 */
func (mm *MigrationManager) GetStatus() ([]Migration, error) {
	allMigrations, err := mm.getAllMigrations()
	if err != nil {
		return nil, err
	}

	appliedVersions, err := mm.getAppliedVersions()
	if err != nil {
		return nil, err
	}

	// 标记已应用的迁移
	appliedMap := make(map[int64]bool)
	for _, version := range appliedVersions {
		appliedMap[version] = true
	}

	for i := range allMigrations {
		if appliedMap[allMigrations[i].Version] {
			now := time.Now()
			allMigrations[i].AppliedAt = &now
		}
	}

	return allMigrations, nil
}

/**
 * 应用单个迁移
 */
func (mm *MigrationManager) applyMigration(migration Migration, isUp bool) error {
	var sql string
	var operation string

	if isUp {
		sql = migration.UpSQL
		operation = "应用"
	} else {
		sql = migration.DownSQL
		operation = "回滚"
	}

	if sql == "" {
		return fmt.Errorf("迁移 %d_%s 的 %s SQL 为空", migration.Version, migration.Name, strings.ToLower(operation))
	}

	// 在事务中执行迁移
	err := WithTransaction(mm.db, func(tm *TransactionManager) error {
		// 执行迁移SQL
		_, err := tm.Exec(sql)
		if err != nil {
			return err
		}

		// 更新迁移记录
		if isUp {
			_, err = tm.Exec(fmt.Sprintf("INSERT INTO %s (version, name) VALUES (?, ?)", mm.tableName),
				migration.Version, migration.Name)
		} else {
			_, err = tm.Exec(fmt.Sprintf("DELETE FROM %s WHERE version = ?", mm.tableName), migration.Version)
		}

		return err
	})

	if err != nil {
		LogError("%s迁移失败 %d_%s: %v", operation, migration.Version, migration.Name, err)
		return err
	}

	LogInfo("%s迁移成功 %d_%s", operation, migration.Version, migration.Name)
	return nil
}

/**
 * 获取待应用的迁移
 */
func (mm *MigrationManager) getPendingMigrations() ([]Migration, error) {
	allMigrations, err := mm.getAllMigrations()
	if err != nil {
		return nil, err
	}

	appliedVersions, err := mm.getAppliedVersions()
	if err != nil {
		return nil, err
	}

	appliedMap := make(map[int64]bool)
	for _, version := range appliedVersions {
		appliedMap[version] = true
	}

	var pending []Migration
	for _, migration := range allMigrations {
		if !appliedMap[migration.Version] {
			pending = append(pending, migration)
		}
	}

	return pending, nil
}

/**
 * 获取已应用的迁移
 */
func (mm *MigrationManager) getAppliedMigrations() ([]Migration, error) {
	query := fmt.Sprintf("SELECT version, name, applied_at FROM %s ORDER BY version", mm.tableName)
	rows, err := mm.db.DataSource.Query(query)
	if err != nil {
		return nil, NewQueryExceptionWithCause(err, "查询已应用迁移失败")
	}
	defer rows.Close()

	var migrations []Migration
	for rows.Next() {
		var migration Migration
		var appliedAt time.Time
		err := rows.Scan(&migration.Version, &migration.Name, &appliedAt)
		if err != nil {
			return nil, NewQueryExceptionWithCause(err, "扫描迁移记录失败")
		}
		migration.AppliedAt = &appliedAt
		migrations = append(migrations, migration)
	}

	return migrations, nil
}

/**
 * 获取所有迁移文件
 */
func (mm *MigrationManager) getAllMigrations() ([]Migration, error) {
	files, err := ioutil.ReadDir(mm.migrationsDir)
	if err != nil {
		return nil, NewConfigurationExceptionWithCause(err, "读取迁移目录失败")
	}

	var migrations []Migration
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".up.sql") {
			migration, err := mm.parseMigrationFile(file.Name())
			if err != nil {
				LogWarn("解析迁移文件失败 %s: %v", file.Name(), err)
				continue
			}
			migrations = append(migrations, migration)
		}
	}

	// 按版本排序
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}

/**
 * 解析迁移文件名
 */
func (mm *MigrationManager) parseMigrationFile(filename string) (Migration, error) {
	// 文件名格式: {version}_{name}.up.sql
	parts := strings.Split(strings.TrimSuffix(filename, ".up.sql"), "_")
	if len(parts) < 2 {
		return Migration{}, fmt.Errorf("无效的迁移文件名: %s", filename)
	}

	version, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return Migration{}, fmt.Errorf("无效的版本号: %s", parts[0])
	}

	name := strings.Join(parts[1:], "_")

	// 读取上迁SQL
	upFile := filepath.Join(mm.migrationsDir, filename)
	upSQL, err := ioutil.ReadFile(upFile)
	if err != nil {
		return Migration{}, fmt.Errorf("读取上迁文件失败: %w", err)
	}

	// 读取下迁SQL
	downFile := strings.Replace(filename, ".up.sql", ".down.sql", 1)
	downFilePath := filepath.Join(mm.migrationsDir, downFile)
	downSQL, err := ioutil.ReadFile(downFilePath)
	if err != nil {
		return Migration{}, fmt.Errorf("读取下迁文件失败: %w", err)
	}

	return Migration{
		Version: version,
		Name:    name,
		UpSQL:   string(upSQL),
		DownSQL: string(downSQL),
	}, nil
}

/**
 * 获取已应用的版本
 */
func (mm *MigrationManager) getAppliedVersions() ([]int64, error) {
	query := fmt.Sprintf("SELECT version FROM %s ORDER BY version", mm.tableName)
	rows, err := mm.db.DataSource.Query(query)
	if err != nil {
		return nil, NewQueryExceptionWithCause(err, "查询已应用版本失败")
	}
	defer rows.Close()

	var versions []int64
	for rows.Next() {
		var version int64
		err := rows.Scan(&version)
		if err != nil {
			return nil, NewQueryExceptionWithCause(err, "扫描版本失败")
		}
		versions = append(versions, version)
	}

	return versions, nil
}

/**
 * 获取当前版本
 */
func (mm *MigrationManager) getCurrentVersion() (int64, error) {
	query := fmt.Sprintf("SELECT COALESCE(MAX(version), 0) FROM %s", mm.tableName)
	row := mm.db.DataSource.QueryRow(query)

	var version int64
	err := row.Scan(&version)
	if err != nil {
		return 0, NewQueryExceptionWithCause(err, "获取当前版本失败")
	}

	return version, nil
}

/**
 * 上迁到指定版本
 */
func (mm *MigrationManager) upToVersion(targetVersion int64) error {
	pendingMigrations, err := mm.getPendingMigrations()
	if err != nil {
		return err
	}

	var migrationsToApply []Migration
	for _, migration := range pendingMigrations {
		if migration.Version <= targetVersion {
			migrationsToApply = append(migrationsToApply, migration)
		}
	}

	for _, migration := range migrationsToApply {
		err := mm.applyMigration(migration, true)
		if err != nil {
			return err
		}
	}

	LogInfo("已上迁到版本: %d", targetVersion)
	return nil
}

/**
 * 下迁到指定版本
 */
func (mm *MigrationManager) downToVersion(targetVersion int64) error {
	appliedMigrations, err := mm.getAppliedMigrations()
	if err != nil {
		return err
	}

	var migrationsToRollback []Migration
	for _, migration := range appliedMigrations {
		if migration.Version > targetVersion {
			migrationsToRollback = append(migrationsToRollback, migration)
		}
	}

	// 反转顺序回滚
	for i := len(migrationsToRollback) - 1; i >= 0; i-- {
		err := mm.applyMigration(migrationsToRollback[i], false)
		if err != nil {
			return err
		}
	}

	LogInfo("已下迁到版本: %d", targetVersion)
	return nil
}
