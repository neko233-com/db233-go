package db233

import (
	"database/sql"
	"fmt"
	"time"
)

/**
 * DbConnectionConfig - 数据库连接配置
 *
 * 支持 MySQL 和 PostgreSQL 的完整配置
 *
 * @author neko233-com
 * @since 2026-01-08
 */
type DbConnectionConfig struct {
	// 基础配置
	DatabaseType DatabaseType `json:"databaseType" yaml:"databaseType"` // 数据库类型
	Host         string       `json:"host" yaml:"host"`                 // 主机地址
	Port         int          `json:"port" yaml:"port"`                 // 端口号
	Username     string       `json:"username" yaml:"username"`         // 用户名
	Password     string       `json:"password" yaml:"password"`         // 密码
	Database     string       `json:"database" yaml:"database"`         // 数据库名

	// 连接池配置
	MaxOpenConns    int           `json:"maxOpenConns" yaml:"maxOpenConns"`       // 最大打开连接数
	MaxIdleConns    int           `json:"maxIdleConns" yaml:"maxIdleConns"`       // 最大空闲连接数
	ConnMaxLifetime time.Duration `json:"connMaxLifetime" yaml:"connMaxLifetime"` // 连接最大生命周期
	ConnMaxIdleTime time.Duration `json:"connMaxIdleTime" yaml:"connMaxIdleTime"` // 连接最大空闲时间

	// 字符集配置（MySQL）
	Charset   string `json:"charset" yaml:"charset"`     // 字符集（默认 utf8mb4）
	Collation string `json:"collation" yaml:"collation"` // 排序规则

	// SSL 配置
	SSLMode     string `json:"sslMode" yaml:"sslMode"`         // SSL 模式（PostgreSQL: disable, require, verify-ca, verify-full）
	SSLCert     string `json:"sslCert" yaml:"sslCert"`         // SSL 证书路径
	SSLKey      string `json:"sslKey" yaml:"sslKey"`           // SSL 私钥路径
	SSLRootCert string `json:"sslRootCert" yaml:"sslRootCert"` // SSL 根证书路径

	// 超时配置
	ConnectTimeout time.Duration `json:"connectTimeout" yaml:"connectTimeout"` // 连接超时
	ReadTimeout    time.Duration `json:"readTimeout" yaml:"readTimeout"`       // 读取超时
	WriteTimeout   time.Duration `json:"writeTimeout" yaml:"writeTimeout"`     // 写入超时

	// 其他配置
	ParseTime       bool              `json:"parseTime" yaml:"parseTime"`             // 是否解析时间（MySQL）
	Loc             string            `json:"loc" yaml:"loc"`                         // 时区（MySQL）
	ExtraParams     map[string]string `json:"extraParams" yaml:"extraParams"`         // 额外参数
	ApplicationName string            `json:"applicationName" yaml:"applicationName"` // 应用名称（PostgreSQL）
}

/**
 * NewDefaultMySQLConfig 创建默认 MySQL 配置
 */
func NewDefaultMySQLConfig(host string, port int, username, password, database string) *DbConnectionConfig {
	return &DbConnectionConfig{
		DatabaseType:    DatabaseTypeMySQL,
		Host:            host,
		Port:            port,
		Username:        username,
		Password:        password,
		Database:        database,
		MaxOpenConns:    100,
		MaxIdleConns:    10,
		ConnMaxLifetime: 3600 * time.Second, // 1小时
		ConnMaxIdleTime: 600 * time.Second,  // 10分钟
		Charset:         "utf8mb4",
		Collation:       "utf8mb4_unicode_ci",
		ParseTime:       true,
		Loc:             "Local",
		ConnectTimeout:  10 * time.Second,
		ReadTimeout:     30 * time.Second,
		WriteTimeout:    30 * time.Second,
		ExtraParams:     make(map[string]string),
	}
}

/**
 * NewDefaultPostgreSQLConfig 创建默认 PostgreSQL 配置
 */
func NewDefaultPostgreSQLConfig(host string, port int, username, password, database string) *DbConnectionConfig {
	return &DbConnectionConfig{
		DatabaseType:    DatabaseTypePostgreSQL,
		Host:            host,
		Port:            port,
		Username:        username,
		Password:        password,
		Database:        database,
		MaxOpenConns:    100,
		MaxIdleConns:    10,
		ConnMaxLifetime: 3600 * time.Second, // 1小时
		ConnMaxIdleTime: 600 * time.Second,  // 10分钟
		SSLMode:         "disable",
		ConnectTimeout:  10 * time.Second,
		ReadTimeout:     30 * time.Second,
		WriteTimeout:    30 * time.Second,
		ApplicationName: "db233-go",
		ExtraParams:     make(map[string]string),
	}
}

/**
 * BuildDSN 构建数据源连接字符串
 */
func (c *DbConnectionConfig) BuildDSN() string {
	switch c.DatabaseType {
	case DatabaseTypeMySQL:
		return c.buildMySQLDSN()
	case DatabaseTypePostgreSQL:
		return c.buildPostgreSQLDSN()
	default:
		return c.buildMySQLDSN()
	}
}

/**
 * buildMySQLDSN 构建 MySQL DSN
 * 格式: username:password@tcp(host:port)/database?charset=utf8mb4&parseTime=True&loc=Local
 */
func (c *DbConnectionConfig) buildMySQLDSN() string {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s",
		c.Username, c.Password, c.Host, c.Port, c.Database)

	params := make(map[string]string)

	// 字符集
	if c.Charset != "" {
		params["charset"] = c.Charset
	}
	if c.Collation != "" {
		params["collation"] = c.Collation
	}

	// 时间解析
	if c.ParseTime {
		params["parseTime"] = "True"
	}
	if c.Loc != "" {
		params["loc"] = c.Loc
	}

	// 超时配置
	if c.ConnectTimeout > 0 {
		params["timeout"] = c.ConnectTimeout.String()
	}
	if c.ReadTimeout > 0 {
		params["readTimeout"] = c.ReadTimeout.String()
	}
	if c.WriteTimeout > 0 {
		params["writeTimeout"] = c.WriteTimeout.String()
	}

	// 额外参数
	for k, v := range c.ExtraParams {
		params[k] = v
	}

	// 构建查询字符串
	if len(params) > 0 {
		dsn += "?"
		first := true
		for k, v := range params {
			if !first {
				dsn += "&"
			}
			dsn += fmt.Sprintf("%s=%s", k, v)
			first = false
		}
	}

	return dsn
}

/**
 * buildPostgreSQLDSN 构建 PostgreSQL DSN
 * 格式: host=localhost port=5432 user=postgres password=postgres dbname=mydb sslmode=disable
 */
func (c *DbConnectionConfig) buildPostgreSQLDSN() string {
	params := make(map[string]string)

	params["host"] = c.Host
	params["port"] = fmt.Sprintf("%d", c.Port)
	params["user"] = c.Username
	params["password"] = c.Password
	params["dbname"] = c.Database

	// SSL 配置
	if c.SSLMode != "" {
		params["sslmode"] = c.SSLMode
	}
	if c.SSLCert != "" {
		params["sslcert"] = c.SSLCert
	}
	if c.SSLKey != "" {
		params["sslkey"] = c.SSLKey
	}
	if c.SSLRootCert != "" {
		params["sslrootcert"] = c.SSLRootCert
	}

	// 超时配置
	if c.ConnectTimeout > 0 {
		params["connect_timeout"] = fmt.Sprintf("%d", int(c.ConnectTimeout.Seconds()))
	}

	// 应用名称
	if c.ApplicationName != "" {
		params["application_name"] = c.ApplicationName
	}

	// 额外参数
	for k, v := range c.ExtraParams {
		params[k] = v
	}

	// 构建连接字符串
	dsn := ""
	first := true
	for k, v := range params {
		if !first {
			dsn += " "
		}
		dsn += fmt.Sprintf("%s=%s", k, v)
		first = false
	}

	return dsn
}

/**
 * CreateDataSource 创建数据源
 */
func (c *DbConnectionConfig) CreateDataSource() (*sql.DB, error) {
	dsn := c.BuildDSN()

	var driverName string
	switch c.DatabaseType {
	case EnumDatabaseTypeMySQL:
		driverName = "mysql"
	case EnumDatabaseTypePostgreSQL:
		driverName = "postgres"
	default:
		driverName = "mysql"
	}

	dataSource, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库连接失败: %w", err)
	}

	// 配置连接池
	if c.MaxOpenConnectionCount > 0 {
		dataSource.SetMaxOpenConns(c.MaxOpenConnectionCount)
	}
	if c.MaxIdleConnectionCount > 0 {
		dataSource.SetMaxIdleConns(c.MaxIdleConnectionCount)
	}
	if c.ConnectionMaxLifetimeSeconds > 0 {
		dataSource.SetConnMaxLifetime(c.ConnectionMaxLifetimeSeconds)
	}
	if c.ConnectionMaxIdleTimeSeconds > 0 {
		dataSource.SetConnMaxIdleTime(c.ConnectionMaxIdleTimeSeconds)
	}

	// 测试连接
	if err := dataSource.Ping(); err != nil {
		dataSource.Close()
		return nil, fmt.Errorf("数据库连接测试失败: %w", err)
	}

	LogInfo("数据库连接成功: 类型=%s, 主机=%s:%d, 数据库=%s", c.DatabaseType, c.Host, c.Port, c.Database)
	return dataSource, nil
}

/**
 * CreateDb 创建 Db 实例
 */
func (c *DbConnectionConfig) CreateDb(dbId int, dbGroup *DbGroup) (*Db, error) {
	dataSource, err := c.CreateDataSource()
	if err != nil {
		return nil, err
	}

	return NewDbWithType(dataSource, dbId, dbGroup, c.DatabaseType), nil
}
