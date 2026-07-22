package db233

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// LocalDbConfigFile 本地开发数据库配置（config.local.json，勿提交 Git）。
type LocalDbConfigFile struct {
	DatabaseType       string `json:"databaseType" yaml:"databaseType"`
	Host               string `json:"host" yaml:"host"`
	Port               int    `json:"port" yaml:"port"`
	Username           string `json:"username" yaml:"username"`
	Password           string `json:"password" yaml:"password"`
	Database           string `json:"database" yaml:"database"`
	MaxOpenConns       int    `json:"maxOpenConns" yaml:"maxOpenConns"`
	MaxIdleConns       int    `json:"maxIdleConns" yaml:"maxIdleConns"`
	ConnMaxLifetimeSec int    `json:"connMaxLifetimeSec" yaml:"connMaxLifetimeSec"`
	ConnMaxIdleTimeSec int    `json:"connMaxIdleTimeSec" yaml:"connMaxIdleTimeSec"`
}

// DefaultLocalConfigPath 默认本地配置文件名。
const DefaultLocalConfigPath = "config.local.json"

// LoadLocalDbConfigFromFile 从 config.local.json 加载连接配置。
func LoadLocalDbConfigFromFile(path string) (*LocalDbConfigFile, error) {
	data, err := readLocalDbConfigFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取本地配置 %s 失败: %w", path, err)
	}
	var cfg LocalDbConfigFile
	if err := decodeLocalDbConfig(path, data, &cfg); err != nil {
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

func decodeLocalDbConfig(path string, data []byte, cfg *LocalDbConfigFile) error {
	lowerPath := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lowerPath, ".yaml"), strings.HasSuffix(lowerPath, ".yml"),
		strings.HasSuffix(lowerPath, ".yaml.example"), strings.HasSuffix(lowerPath, ".yml.example"):
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(cfg); err != nil {
			return err
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			if err == nil {
				return errors.New("本地 YAML 配置只能包含一个文档")
			}
			return err
		}
		return nil
	default:
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(cfg); err != nil {
			return err
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			if err == nil {
				return errors.New("本地 JSON 配置只能包含一个对象")
			}
			return err
		}
		return nil
	}
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
	ctx, cancel := context.WithTimeout(context.Background(), DefaultConnectionTimeout)
	defer cancel()
	return OpenDbFromLocalConfigContext(ctx, path)
}

// OpenDbFromLocalConfigContext 从本地配置创建 Db，并严格传播连接/预热/回滚错误。
func OpenDbFromLocalConfigContext(ctx context.Context, path string) (*Db, *DbConnectionConfig, error) {
	if ctx == nil {
		return nil, nil, NewValidationException("打开本地数据库 context 不能为 nil")
	}
	local, err := LoadLocalDbConfigFromFile(path)
	if err != nil {
		return nil, nil, err
	}
	dbConfig := local.ToDbConnectionConfig()
	db, err := dbConfig.CreateDbContext(ctx, 0, nil)
	if err != nil {
		return nil, nil, err
	}
	RegisterDbForConnectionPool(db)
	if err := WarmConnectionPoolContext(ctx, db.DataSource, 2); err != nil {
		UnregisterDbForConnectionPool(db)
		closeErr := db.Close()
		return nil, nil, errors.Join(fmt.Errorf("预热本地数据库连接: %w", err), closeErr)
	}
	return db, dbConfig, nil
}
