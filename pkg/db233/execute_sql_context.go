package db233

import (
	"time"
)

/**
 * ExecuteSqlContext - SQL 执行上下文
 *
 * 对应 Kotlin 版本的 ExecuteSqlContext
 * 包含 SQL 执行的上下文信息
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
type ExecuteSqlContext struct {
	// SQL 语句
	Sql string

	// SQL 参数
	Params []interface{}

	// 执行开始时间
	StartTime time.Time

	// 执行结束时间
	EndTime time.Time

	// 执行耗时
	Duration time.Duration

	// 影响行数
	AffectedRows int

	// 执行结果
	Result interface{}

	// 错误信息
	Error error

	// 数据库连接信息
	DataSource interface{}

	// 其他上下文信息
	Attributes map[string]interface{}
}

/**
 * 创建新的 SQL 执行上下文
 */
func NewExecuteSqlContext(sql string, params []interface{}) *ExecuteSqlContext {
	return &ExecuteSqlContext{
		Sql:        sql,
		Params:     params,
		StartTime:  time.Now(),
		Attributes: make(map[string]interface{}),
	}
}

/**
 * 标记执行开始
 */
func (ctx *ExecuteSqlContext) MarkStart() {
	ctx.StartTime = time.Now()
}

/**
 * 标记执行结束
 */
func (ctx *ExecuteSqlContext) MarkEnd() {
	ctx.EndTime = time.Now()
	ctx.Duration = ctx.EndTime.Sub(ctx.StartTime)
}

/**
 * 设置执行结果
 */
func (ctx *ExecuteSqlContext) SetResult(result interface{}, affectedRows int) {
	ctx.Result = result
	ctx.AffectedRows = affectedRows
	ctx.MarkEnd()
}

/**
 * 设置执行错误
 */
func (ctx *ExecuteSqlContext) SetError(err error) {
	ctx.Error = err
	ctx.MarkEnd()
}

/**
 * 获取属性
 */
func (ctx *ExecuteSqlContext) GetAttribute(key string) interface{} {
	return ctx.Attributes[key]
}

/**
 * 设置属性
 */
func (ctx *ExecuteSqlContext) SetAttribute(key string, value interface{}) {
	ctx.Attributes[key] = value
}
