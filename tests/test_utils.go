package tests

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/neko233-com/db233-go/pkg/db233"
)

// localConfigCandidates 从 tests/ 目录向上查找 config.local.json。
var localConfigCandidates = []string{
	"config.local.json",
	filepath.Join("..", "config.local.json"),
	filepath.Join("..", "..", "config.local.json"),
}

// LoadLocalDbConfig 加载本地 config.local.json（存在则返回配置，否则 nil）。
func LoadLocalDbConfig() (*db233.LocalDbConfigFile, string) {
	for _, p := range localConfigCandidates {
		cfg, err := db233.LoadLocalDbConfigFromFile(p)
		if err == nil {
			return cfg, p
		}
	}
	return nil, ""
}

// CreateTestDb 创建测试数据库连接。
// 普通集成测试固定使用本地 MySQL，避免误连远程数据库。
func CreateTestDb(t *testing.T) *db233.Db {
	// 创建 SQL 数据库连接 (不指定数据库，使用默认)
	dataSource, err := sql.Open("mysql", "root:root@tcp(127.0.0.1:3306)/")
	if err != nil {
		t.Skipf("无法打开数据库连接: %v", err)
		return nil
	}

	// 创建测试数据库
	_, err = dataSource.Exec("CREATE DATABASE IF NOT EXISTS db233_go CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci")
	if err != nil {
		t.Skipf("无法创建测试数据库: %v", err)
		dataSource.Close()
		return nil
	}

	// 重新连接到指定数据库
	dataSource.Close()
	dataSource, err = sql.Open("mysql", "root:root@tcp(127.0.0.1:3306)/db233_go?parseTime=true&loc=Local&charset=utf8mb4")
	if err != nil {
		t.Skipf("无法连接到测试数据库: %v", err)
		return nil
	}

	// 测试连接
	if err := dataSource.Ping(); err != nil {
		t.Skipf("数据库连接测试失败: %v", err)
		dataSource.Close()
		return nil
	}

	// 创建 Db 实例
	db := db233.NewDb(dataSource, 0, nil)
	db233.RegisterDbForConnectionPool(db)
	_ = db233.WarmConnectionPool(db.DataSource, 2)
	return db
}

// ensureDatabaseExists 连接实例并在目标库不存在时自动创建（显式本地配置测试用）。
func ensureDatabaseExists(cfg *db233.DbConnectionConfig) error {
	if cfg == nil || cfg.Database == "" {
		return nil
	}
	bootstrap := *cfg
	bootstrap.Database = ""
	ds, err := bootstrap.CreateDataSource()
	if err != nil {
		return err
	}
	defer ds.Close()
	_, err = ds.Exec(fmt.Sprintf(
		"CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci",
		cfg.Database,
	))
	return err
}

// CreateTestDbFromLocalConfig 仅从 config.local.json 创建连接，无回退。
func CreateTestDbFromLocalConfig(t *testing.T) *db233.Db {
	path := db233.DefaultLocalConfigPath
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join("..", db233.DefaultLocalConfigPath)
	}
	db, _, err := db233.OpenDbFromLocalConfig(path)
	if err != nil {
		t.Skipf("config.local.json 不可用: %v", err)
		return nil
	}
	return db
}

// SetupTestTables 设置测试表结构
func SetupTestTables(db *db233.Db) error {
	// 创建测试用户表
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS test_user (
			id INT AUTO_INCREMENT PRIMARY KEY,
			username VARCHAR(255) NOT NULL,
			email VARCHAR(255) NOT NULL,
			age INT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`

	_, err := db.DataSource.Exec(createTableSQL)
	if err != nil {
		return err
	}

	// 清理旧数据
	cleanupSQL := "DELETE FROM test_user WHERE username LIKE 'test%' OR username LIKE 'find%' OR username LIKE 'update%' OR username LIKE 'delete%' OR username LIKE 'count%'"
	_, err = db.DataSource.Exec(cleanupSQL)
	return err
}

// CleanupTestTables 清理测试表
func CleanupTestTables(db *db233.Db) error {
	dropSQL := "DROP TABLE IF EXISTS test_user"
	_, err := db.DataSource.Exec(dropSQL)
	return err
}

// TestUser 测试用户结构体
type TestUser struct {
	ID       int    `db:"id" primary_key:"true" auto_increment:"true"`
	Username string `db:"username"`
	Email    string `db:"email"`
	Age      int    `db:"age"`
}

// TableName 实现 IDbEntity 接口 - 获取表名
func (u *TestUser) TableName() string {
	return "test_user"
}

// SerializeBeforeSaveDb 实现 IDbEntity 接口 - 保存前的序列化钩子
func (u *TestUser) SerializeBeforeSaveDb() {
	// 测试中不需要特殊处理，留空即可
}

// DeserializeAfterLoadDb 实现 IDbEntity 接口 - 加载后的反序列化钩子
func (u *TestUser) DeserializeAfterLoadDb() {
	// 测试中不需要特殊处理，留空即可
}
