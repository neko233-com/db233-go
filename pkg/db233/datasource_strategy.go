package db233

import (
	"database/sql"
	"database/sql/driver"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
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
	// 合并配置
	merged := make(map[string]any)
	for k, v := range template {
		merged[k] = v
	}
	for k, v := range config {
		merged[k] = v
	}

	// 构建连接字符串
	// 假设是 MySQL
	dsn := fmt.Sprintf("%v:%v@tcp(%v:%v)/%v?%v",
		merged["username"],
		merged["password"],
		merged["host"],
		merged["port"],
		merged["database"],
		merged["params"],
	)

	// 打开数据库连接
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}

	// 测试连接
	if err := db.Ping(); err != nil {
		return nil, err
	}

	return db.Driver(), nil
}

// 单例实例
var SimpleDataSourceCreateStrategyInstance = &SimpleDataSourceCreateStrategy{}
