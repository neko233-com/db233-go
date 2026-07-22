package tests

import (
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
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

// CreateTestDb 创建测试数据库连接。连接只能来自 DB233_TEST_DSN 或未纳入
// Git 的 config.local.json，且必须指向 loopback/本机 Unix socket。
func CreateTestDb(t *testing.T) *db233.Db {
	t.Helper()

	var dataSource *sql.DB
	var err error
	if dsn := os.Getenv("DB233_TEST_DSN"); dsn != "" {
		requireLocalTestDSN(t, dsn)
		dataSource, err = sql.Open("mysql", dsn)
	} else if cfg, _ := LoadLocalDbConfig(); cfg != nil {
		dbCfg := cfg.ToDbConnectionConfig()
		requireLoopbackHost(t, dbCfg.Host)
		if err = ensureDatabaseExists(dbCfg); err == nil {
			dataSource, err = dbCfg.CreateDataSource()
		}
	} else {
		t.Skip("未配置 DB233_TEST_DSN 或忽略提交的 config.local.json")
		return nil
	}
	if err != nil {
		t.Skipf("无法连接测试数据库: %v", err)
		return nil
	}

	// 测试连接
	if err := dataSource.Ping(); err != nil {
		t.Skipf("数据库连接测试失败: %v", err)
		_ = dataSource.Close()
		return nil
	}

	// 创建 Db 实例
	db := db233.NewDb(dataSource, 0, nil)
	db233.RegisterDbForConnectionPool(db)
	_ = db233.WarmConnectionPool(db.DataSource, 2)
	return db
}

func requireLocalTestDSN(t *testing.T, dsn string) {
	t.Helper()
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("DB233_TEST_DSN 非法: %v", err)
	}
	if cfg.Net == "unix" {
		return
	}
	host, _, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		t.Fatalf("DB233_TEST_DSN 地址非法: %v", err)
	}
	requireLoopbackHost(t, host)
}

func requireLoopbackHost(t *testing.T, host string) {
	t.Helper()
	if !isLoopbackTestHost(host) {
		t.Fatalf("集成测试拒绝连接非本机数据库 host=%q", host)
	}
}

func isLoopbackTestHost(host string) bool {
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
	defer func() {
		// Bootstrap connection cleanup cannot change the CREATE DATABASE result.
		_ = ds.Close()
	}()
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
