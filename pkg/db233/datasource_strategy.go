package db233

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"strings"

	mysql "github.com/go-sql-driver/mysql"
)

// SimpleDataSourceCreateStrategy - 简单数据源创建策略
// 对应 Kotlin 版本的 DruidDataSourceCreateStrategy
// 使用 Go 标准库 sql.DB
type SimpleDataSourceCreateStrategy struct{}

// 策略名称
// 返回: string
func (s *SimpleDataSourceCreateStrategy) Name() string {
	return "simple"
}

// 创建数据源
// template: 模板配置
// config: 具体配置
// 返回: driver.Driver 数据源驱动
// 返回: error 创建错误
func (s *SimpleDataSourceCreateStrategy) Create(template map[string]any, config map[string]any) (driver.Driver, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultConnectionTimeout)
	defer cancel()
	return s.CreateContext(ctx, template, config)
}

// CreateContext 使用可取消 Ping 验证连接配置，并严格传播临时连接池 Close 错误。
func (s *SimpleDataSourceCreateStrategy) CreateContext(
	ctx context.Context,
	template map[string]any,
	config map[string]any,
) (driver.Driver, error) {
	if ctx == nil {
		return nil, NewValidationException("创建数据源策略 context 不能为 nil")
	}
	driverConfig, err := simpleMySQLDriverConfig(template, config)
	if err != nil {
		return nil, err
	}
	connector, err := mysql.NewConnector(driverConfig)
	if err != nil {
		return nil, NewConfigurationExceptionWithCause(err, "创建 MySQL Connector 失败")
	}
	db := sql.OpenDB(connector)
	if err := db.PingContext(ctx); err != nil {
		closeErr := db.Close()
		return nil, NewConnectionExceptionWithCause(errors.Join(err, closeErr), "测试 MySQL 连接失败")
	}
	if err := db.Close(); err != nil {
		return nil, NewConnectionExceptionWithCause(err, "关闭 MySQL 探测连接失败")
	}
	// 返回绑定 Connector 的 Driver；Open 的 name 参数不会重新解析凭据。
	return &connectorBoundDriver{connector: connector}, nil
}

type connectorBoundDriver struct {
	connector driver.Connector
}

func (d *connectorBoundDriver) Open(string) (driver.Conn, error) {
	if d == nil || d.connector == nil {
		return nil, NewConfigurationException("数据源 Connector 未初始化")
	}
	ctx, cancel := context.WithTimeout(context.Background(), DefaultConnectionTimeout)
	defer cancel()
	return d.connector.Connect(ctx)
}

func (d *connectorBoundDriver) OpenConnector(string) (driver.Connector, error) {
	if d == nil || d.connector == nil {
		return nil, NewConfigurationException("数据源 Connector 未初始化")
	}
	return d.connector, nil
}

func simpleMySQLDriverConfig(template map[string]any, config map[string]any) (*mysql.Config, error) {
	merged := make(map[string]any, len(template)+len(config))
	for key, value := range template {
		merged[key] = value
	}
	for key, value := range config {
		merged[key] = value
	}
	port, err := toInt(merged["port"])
	if err != nil {
		return nil, NewConfigurationExceptionWithCause(err, "MySQL 端口非法")
	}
	connectionConfig := NewDefaultMySQLConfig(
		configStringValue(merged["host"]),
		port,
		configStringValue(merged["username"]),
		configStringValue(merged["password"]),
		configStringValue(merged["database"]),
	)
	params, err := simpleDataSourceParams(merged["params"])
	if err != nil {
		return nil, err
	}
	connectionConfig.ExtraParams = params
	return connectionConfig.mysqlDriverConfig()
}

func simpleDataSourceParams(value any) (map[string]string, error) {
	result := make(map[string]string)
	switch params := value.(type) {
	case nil:
		return result, nil
	case string:
		params = strings.TrimPrefix(strings.TrimSpace(params), "?")
		if params == "" {
			return result, nil
		}
		values, err := url.ParseQuery(params)
		if err != nil {
			return nil, NewConfigurationExceptionWithCause(err, "MySQL params 非法")
		}
		for key, items := range values {
			if len(items) > 0 {
				result[key] = items[len(items)-1]
			}
		}
	case map[string]string:
		for key, item := range params {
			result[key] = item
		}
	case map[string]any:
		for key, item := range params {
			result[key] = configStringValue(item)
		}
	default:
		return nil, NewConfigurationException(fmt.Sprintf("MySQL params 类型非法: %T", value))
	}
	return result, nil
}

func configStringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

// 单例实例
var SimpleDataSourceCreateStrategyInstance = &SimpleDataSourceCreateStrategy{}
