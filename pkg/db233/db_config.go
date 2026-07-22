package db233

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

// DefaultConnectionTimeout bounds compatibility constructors without a
// caller-provided context.
const DefaultConnectionTimeout = 30 * time.Second

// DbConnectionConfig - 数据库连接配置
// 支持 MySQL 和 PostgreSQL 的完整配置
type DbConnectionConfig struct {
	// 基础配置
	DatabaseType EnumDatabaseType `json:"databaseType" yaml:"databaseType"` // 数据库类型
	Host         string           `json:"host" yaml:"host"`                 // 主机地址
	Port         int              `json:"port" yaml:"port"`                 // 端口号
	Username     string           `json:"username" yaml:"username"`         // 用户名
	Password     string           `json:"password" yaml:"password"`         // 密码
	Database     string           `json:"database" yaml:"database"`         // 数据库名

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

// NewDefaultMySQLConfig 创建默认 MySQL 配置
func NewDefaultMySQLConfig(host string, port int, username, password, database string) *DbConnectionConfig {
	return &DbConnectionConfig{
		DatabaseType:    EnumDatabaseTypeMySQL,
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

// NewDefaultPostgreSQLConfig 创建默认 PostgreSQL 配置
func NewDefaultPostgreSQLConfig(host string, port int, username, password, database string) *DbConnectionConfig {
	return &DbConnectionConfig{
		DatabaseType:    EnumDatabaseTypePostgreSQL,
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

// BuildDSN 构建数据源连接字符串
func (c *DbConnectionConfig) BuildDSN() string {
	dsn, err := c.BuildDSNStrict()
	if err != nil {
		LogError("构建数据库 DSN 失败: %s", safeErrorForLog(err))
		return ""
	}
	return dsn
}

// BuildDSNStrict 构建 DSN 并传播配置错误。返回值包含凭据，禁止记录。
func (c *DbConnectionConfig) BuildDSNStrict() (string, error) {
	if c == nil {
		return "", NewConfigurationException("数据库连接配置不能为空")
	}
	switch c.DatabaseType {
	case EnumDatabaseTypeMySQL, "":
		driverConfig, err := c.mysqlDriverConfig()
		if err != nil {
			return "", err
		}
		return driverConfig.FormatDSN(), nil
	case EnumDatabaseTypePostgreSQL:
		return c.postgreSQLDSN()
	default:
		return "", NewConfigurationException(fmt.Sprintf("不支持的数据库类型: %q", c.DatabaseType))
	}
}

// buildMySQLDSN 构建 MySQL DSN
// 格式: username:password@tcp(host:port)/database?charset=utf8mb4&parseTime=True&loc=Local
func (c *DbConnectionConfig) buildMySQLDSN() string {
	driverConfig, err := c.mysqlDriverConfig()
	if err != nil {
		return ""
	}
	return driverConfig.FormatDSN()
}

// mysqlDriverConfig 先让官方驱动解析连接参数，再单独设置凭据。
// CreateDataSource 直接使用 Connector，不会把含分隔符的凭据重新拼接、解析。
func (c *DbConnectionConfig) mysqlDriverConfig() (*mysql.Config, error) {
	if c == nil {
		return nil, NewConfigurationException("数据库连接配置不能为空")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return nil, NewConfigurationException(fmt.Sprintf("MySQL 端口非法: %d", c.Port))
	}
	params := make(map[string]string, len(c.ExtraParams)+8)
	if c.Charset != "" {
		params["charset"] = c.Charset
	}
	if c.Collation != "" {
		params["collation"] = c.Collation
	}
	if c.ParseTime {
		params["parseTime"] = "true"
	}
	if c.Loc != "" {
		params["loc"] = c.Loc
	}
	if c.ConnectTimeout > 0 {
		params["timeout"] = c.ConnectTimeout.String()
	}
	if c.ReadTimeout > 0 {
		params["readTimeout"] = c.ReadTimeout.String()
	}
	if c.WriteTimeout > 0 {
		params["writeTimeout"] = c.WriteTimeout.String()
	}
	for key, value := range c.ExtraParams {
		if err := validateConnectionParameterKey(key); err != nil {
			return nil, err
		}
		params[key] = value
	}

	values := make(url.Values, len(params))
	for key, value := range params {
		values.Set(key, value)
	}
	address := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	dsn := "tcp(" + address + ")/" + url.PathEscape(c.Database)
	if encoded := values.Encode(); encoded != "" {
		dsn += "?" + encoded
	}
	driverConfig, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, NewConfigurationExceptionWithCause(err, "MySQL 连接参数非法")
	}
	driverConfig.User = c.Username
	driverConfig.Passwd = c.Password
	return driverConfig, nil
}

// buildPostgreSQLDSN 构建 PostgreSQL DSN
// 格式: host=localhost port=5432 user=postgres password=postgres dbname=mydb sslmode=disable
func (c *DbConnectionConfig) buildPostgreSQLDSN() string {
	dsn, err := c.postgreSQLDSN()
	if err != nil {
		LogError("构建 PostgreSQL DSN 失败: %s", safeErrorForLog(err))
		return ""
	}
	return dsn
}

func (c *DbConnectionConfig) postgreSQLDSN() (string, error) {
	if c == nil {
		return "", NewConfigurationException("数据库连接配置不能为空")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return "", NewConfigurationException(fmt.Sprintf("PostgreSQL 端口非法: %d", c.Port))
	}
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
		seconds := c.ConnectTimeout / time.Second
		if c.ConnectTimeout%time.Second != 0 {
			seconds++
		}
		params["connect_timeout"] = strconv.FormatInt(int64(seconds), 10)
	}

	// 应用名称
	if c.ApplicationName != "" {
		params["application_name"] = c.ApplicationName
	}

	// 额外参数
	for k, v := range c.ExtraParams {
		if err := validateConnectionParameterKey(k); err != nil {
			return "", err
		}
		params[k] = v
	}

	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for index, key := range keys {
		if index > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(quotePostgreSQLDSNValue(params[key]))
	}
	return builder.String(), nil
}

func validateConnectionParameterKey(key string) error {
	if key == "" {
		return NewConfigurationException("数据库连接扩展参数名不能为空")
	}
	for index := 0; index < len(key); index++ {
		current := key[index]
		if current == '_' || current >= 'a' && current <= 'z' || current >= 'A' && current <= 'Z' || index > 0 && current >= '0' && current <= '9' {
			continue
		}
		return NewConfigurationException(fmt.Sprintf("数据库连接扩展参数名非法: %q", key))
	}
	return nil
}

func quotePostgreSQLDSNValue(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `'`, `\'`)
	return `'` + escaped + `'`
}

// CreateDataSource 创建数据源
func (c *DbConnectionConfig) CreateDataSource() (*sql.DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultConnectionTimeout)
	defer cancel()
	return c.CreateDataSourceContext(ctx)
}

// CreateDataSourceContext 创建数据源，并用 context 限制首次连接验证。
func (c *DbConnectionConfig) CreateDataSourceContext(ctx context.Context) (*sql.DB, error) {
	if ctx == nil {
		return nil, NewValidationException("创建数据源 context 不能为 nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil {
		return nil, NewConfigurationException("数据库连接配置不能为空")
	}
	var dataSource *sql.DB
	switch c.DatabaseType {
	case EnumDatabaseTypeMySQL, "":
		driverConfig, err := c.mysqlDriverConfig()
		if err != nil {
			return nil, err
		}
		connector, err := mysql.NewConnector(driverConfig)
		if err != nil {
			return nil, NewConfigurationExceptionWithCause(err, "创建 MySQL Connector 失败")
		}
		dataSource = sql.OpenDB(connector)
	case EnumDatabaseTypePostgreSQL:
		dsn, err := c.postgreSQLDSN()
		if err != nil {
			return nil, err
		}
		dataSource, err = sql.Open("postgres", dsn)
		if err != nil {
			return nil, NewConnectionExceptionWithCause(err, "打开数据库连接失败")
		}
	default:
		return nil, NewConfigurationException(fmt.Sprintf("不支持的数据库类型: %q", c.DatabaseType))
	}

	// 配置连接池
	if c.MaxOpenConns > 0 {
		dataSource.SetMaxOpenConns(c.MaxOpenConns)
	}
	if c.MaxIdleConns > 0 {
		dataSource.SetMaxIdleConns(c.MaxIdleConns)
	}
	if c.ConnMaxLifetime > 0 {
		dataSource.SetConnMaxLifetime(c.ConnMaxLifetime)
	}
	if c.ConnMaxIdleTime > 0 {
		dataSource.SetConnMaxIdleTime(c.ConnMaxIdleTime)
	}

	// 测试连接
	if pingErr := dataSource.PingContext(ctx); pingErr != nil {
		closeErr := dataSource.Close()
		if closeErr != nil {
			closeErr = NewConnectionExceptionWithCause(closeErr, "连接测试失败后关闭数据源失败")
		}
		return nil, errors.Join(NewConnectionExceptionWithCause(pingErr, "数据库连接测试失败"), closeErr)
	}

	LogInfo(
		"数据库连接成功: 类型=%s, 主机=%s:%d, 数据库=%s",
		c.DatabaseType,
		safeValueForLog(c.Host),
		c.Port,
		safeValueForLog(c.Database),
	)
	return dataSource, nil
}

// CreateDb 创建 Db 实例
func (c *DbConnectionConfig) CreateDb(dbId int, dbGroup *DbGroup) (*Db, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultConnectionTimeout)
	defer cancel()
	return c.CreateDbContext(ctx, dbId, dbGroup)
}

// CreateDbContext 创建 Db，并用 context 限制连接建立与 Ping。
func (c *DbConnectionConfig) CreateDbContext(ctx context.Context, dbId int, dbGroup *DbGroup) (*Db, error) {
	dataSource, err := c.CreateDataSourceContext(ctx)
	if err != nil {
		return nil, err
	}
	db := NewDbWithType(dataSource, dbId, dbGroup, c.DatabaseType)
	if err := db.EnableFaultToleranceStrict(c); err != nil {
		closeErr := dataSource.Close()
		if closeErr != nil {
			closeErr = NewConnectionExceptionWithCause(closeErr, "容错管理器启动失败后关闭数据库连接失败")
		}
		return nil, errors.Join(err, closeErr)
	}
	return db, nil
}

// CreateDbWithoutFaultTolerance 创建 Db 实例（不启用容错）
func (c *DbConnectionConfig) CreateDbWithoutFaultTolerance(dbId int, dbGroup *DbGroup) (*Db, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultConnectionTimeout)
	defer cancel()
	return c.CreateDbWithoutFaultToleranceContext(ctx, dbId, dbGroup)
}

// CreateDbWithoutFaultToleranceContext 创建不启用容错的 Db，并限制连接验证。
func (c *DbConnectionConfig) CreateDbWithoutFaultToleranceContext(ctx context.Context, dbId int, dbGroup *DbGroup) (*Db, error) {
	dataSource, err := c.CreateDataSourceContext(ctx)
	if err != nil {
		return nil, err
	}

	return NewDbWithType(dataSource, dbId, dbGroup, c.DatabaseType), nil
}
