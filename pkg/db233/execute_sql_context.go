package db233

import (
	"sync"
	"time"
)

// ExecuteSqlContext - SQL 执行上下文
// 对应 Kotlin 版本的 ExecuteSqlContext
// 包含 SQL 执行的上下文信息
//
// Params 和 Attributes 为兼容旧 API 继续导出。访问器彼此可安全并发；一旦上下文被并发共享，
// 调用方必须只使用访问器，不能同时直接读写对应导出字段。访问器只保护 slice/map 结构，
// 不会深复制其中的可变对象。
type ExecuteSqlContext struct {
	// SQL 语句
	Sql string

	// SQL 参数。直接读写仅适用于单协程；并发共享时必须只使用 GetParams/SetParams/ParamCount。
	Params []any

	// 执行开始时间
	StartTime time.Time

	// 执行结束时间
	EndTime time.Time

	// 执行耗时
	Duration time.Duration

	// 影响行数
	AffectedRows int

	// 执行结果
	Result any

	// 错误信息
	Error error

	// 数据库连接信息
	DataSource any

	// 其他上下文信息。直接读写仅适用于单协程；并发共享时必须只使用属性访问器。
	Attributes map[string]any

	accessMu sync.RWMutex
}

// 创建新的 SQL 执行上下文
func NewExecuteSqlContext(sql string, params []any) *ExecuteSqlContext {
	return &ExecuteSqlContext{
		Sql:        sql,
		Params:     cloneAnySlice(params),
		StartTime:  time.Now(),
		Attributes: make(map[string]any),
	}
}

// 标记执行开始
func (ctx *ExecuteSqlContext) MarkStart() {
	ctx.StartTime = time.Now()
}

// 标记执行结束
func (ctx *ExecuteSqlContext) MarkEnd() {
	ctx.EndTime = time.Now()
	ctx.Duration = ctx.EndTime.Sub(ctx.StartTime)
}

// 设置执行结果
func (ctx *ExecuteSqlContext) SetResult(result any, affectedRows int) {
	ctx.Result = result
	ctx.AffectedRows = affectedRows
	ctx.MarkEnd()
}

// 设置执行错误
func (ctx *ExecuteSqlContext) SetError(err error) {
	ctx.Error = err
	ctx.MarkEnd()
}

// GetAttribute 获取属性。返回的可变对象仍由调用方负责同步。
func (ctx *ExecuteSqlContext) GetAttribute(key string) any {
	ctx.accessMu.RLock()
	defer ctx.accessMu.RUnlock()
	return ctx.Attributes[key]
}

// SetAttribute 设置属性。可变 value 会按原值存储，不做深复制。
func (ctx *ExecuteSqlContext) SetAttribute(key string, value any) {
	ctx.accessMu.Lock()
	defer ctx.accessMu.Unlock()
	if ctx.Attributes == nil {
		ctx.Attributes = make(map[string]any)
	}
	ctx.Attributes[key] = value
}

// GetAttributes 获取浅复制属性快照，返回的 map 可由调用方安全修改。
func (ctx *ExecuteSqlContext) GetAttributes() map[string]any {
	ctx.accessMu.RLock()
	defer ctx.accessMu.RUnlock()

	attributes := make(map[string]any, len(ctx.Attributes))
	for key, value := range ctx.Attributes {
		attributes[key] = value
	}
	return attributes
}

// SetAttributes 替换属性；输入 map 会被浅复制，后续结构修改不会影响上下文。
func (ctx *ExecuteSqlContext) SetAttributes(attributes map[string]any) {
	cloned := make(map[string]any, len(attributes))
	for key, value := range attributes {
		cloned[key] = value
	}

	ctx.accessMu.Lock()
	ctx.Attributes = cloned
	ctx.accessMu.Unlock()
}

// GetParams 获取 SQL 参数 slice 的浅复制；返回 slice 可修改，其中的可变元素仍共享。
func (ctx *ExecuteSqlContext) GetParams() []any {
	ctx.accessMu.RLock()
	defer ctx.accessMu.RUnlock()
	return cloneAnySlice(ctx.Params)
}

// SetParams 替换 SQL 参数；输入 slice 会被浅复制。
func (ctx *ExecuteSqlContext) SetParams(params []any) {
	cloned := cloneAnySlice(params)
	ctx.accessMu.Lock()
	ctx.Params = cloned
	ctx.accessMu.Unlock()
}

// ParamCount 获取 SQL 参数数量。
func (ctx *ExecuteSqlContext) ParamCount() int {
	ctx.accessMu.RLock()
	defer ctx.accessMu.RUnlock()
	return len(ctx.Params)
}

func cloneAnySlice(values []any) []any {
	if values == nil {
		return nil
	}
	return append([]any(nil), values...)
}
