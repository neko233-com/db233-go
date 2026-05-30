package db233

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// LocalDbConfigFile 本地开发数据库配置（config.local.json，勿提交 Git）。
type LocalDbConfigFile struct {
	DatabaseType       string `json:"databaseType"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	Username           string `json:"username"`
	Password           string `json:"password"`
	Database           string `json:"database"`
	MaxOpenConns       int    `json:"maxOpenConns"`
	MaxIdleConns       int    `json:"maxIdleConns"`
	ConnMaxLifetimeSec int    `json:"connMaxLifetimeSec"`
	ConnMaxIdleTimeSec int    `json:"connMaxIdleTimeSec"`
}

// DefaultLocalConfigPath 默认本地配置文件名。
const DefaultLocalConfigPath = "config.local.json"

// LoadLocalDbConfigFromFile 从 config.local.json 加载连接配置。
func LoadLocalDbConfigFromFile(path string) (*LocalDbConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取本地配置 %s 失败: %w", path, err)
	}
	var cfg LocalDbConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析本地配置 %s 失败: %w", path, err)
	}
	if cfg.Host == "" {
		return nil, fmt.Errorf("本地配置 %s 缺少 host", path)
	}
	if cfg.Port <= 0 {
		cfg.Port = 3306
	}
	return &cfg, nil
}

// ToDbConnectionConfig 转为 DbConnectionConfig（含连接池，等价 Java HikariCP 参数）。
func (c *LocalDbConfigFile) ToDbConnectionConfig() *DbConnectionConfig {
	dbType := EnumDatabaseTypeMySQL
	if c.DatabaseType == "postgresql" || c.DatabaseType == "postgres" {
		dbType = EnumDatabaseTypePostgreSQL
	}
	cfg := &DbConnectionConfig{
		DatabaseType: dbType,
		Host:         c.Host,
		Port:         c.Port,
		Username:     c.Username,
		Password:     c.Password,
		Database:     c.Database,
		Charset:      "utf8mb4",
		Collation:    "utf8mb4_unicode_ci",
		ParseTime:    true,
		Loc:          "Local",
	}
	if c.MaxOpenConns > 0 {
		cfg.MaxOpenConns = c.MaxOpenConns
	} else {
		cfg.MaxOpenConns = 50
	}
	if c.MaxIdleConns > 0 {
		cfg.MaxIdleConns = c.MaxIdleConns
	} else {
		cfg.MaxIdleConns = 10
	}
	if c.ConnMaxLifetimeSec > 0 {
		cfg.ConnMaxLifetime = time.Duration(c.ConnMaxLifetimeSec) * time.Second
	} else {
		cfg.ConnMaxLifetime = 3600 * time.Second
	}
	if c.ConnMaxIdleTimeSec > 0 {
		cfg.ConnMaxIdleTime = time.Duration(c.ConnMaxIdleTimeSec) * time.Second
	} else {
		cfg.ConnMaxIdleTime = 600 * time.Second
	}
	return cfg
}

// OpenDbFromLocalConfig 从 config.local.json 创建带连接池的 *Db。
func OpenDbFromLocalConfig(path string) (*Db, *DbConnectionConfig, error) {
	local, err := LoadLocalDbConfigFromFile(path)
	if err != nil {
		return nil, nil, err
	}
	dbConfig := local.ToDbConnectionConfig()
	db, err := dbConfig.CreateDb(0, nil)
	if err != nil {
		return nil, nil, err
	}
	RegisterDbForConnectionPool(db)
	_ = WarmConnectionPool(db.DataSource, 2)
	return db, dbConfig, nil
}
