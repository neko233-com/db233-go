package db233

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

const defaultTransactionTimeout = 30 * time.Second

type transactionSavepoint struct {
	name                  string
	postCommitActionCount int
}

// TransactionManager - 事务管理器
// 提供事务管理和分布式事务支持
type TransactionManager struct {
	db     *Db
	tx     *sql.Tx
	txCtx  context.Context
	cancel context.CancelFunc

	// operationMu 串行化同一事务上的 Repository 操作，并与终态操作互斥。
	operationMu       sync.Mutex
	postCommitActions []func()

	// 事务状态
	isActive  bool
	startTime time.Time
	timeout   time.Duration

	// 保存点管理
	savepoints []transactionSavepoint

	// 锁
	mu sync.RWMutex

	// 事务选项
	isolation sql.IsolationLevel
	readOnly  bool
}

// TransactionOptions - 事务选项
type TransactionOptions struct {
	Isolation sql.IsolationLevel
	ReadOnly  bool
	Timeout   time.Duration
}

// 创建事务管理器
func NewTransactionManager(db *Db) *TransactionManager {
	return &TransactionManager{
		db:        db,
		timeout:   defaultTransactionTimeout,
		isolation: sql.LevelDefault,
	}
}

// 开始事务
func (tm *TransactionManager) Begin(opts ...TransactionOptions) error {
	return tm.BeginContext(context.Background(), opts...)
}

// BeginContext 使用调用方上下文开始事务。
// 事务上下文会保留到 Commit 或 Rollback 完成后再释放。
func (tm *TransactionManager) BeginContext(ctx context.Context, opts ...TransactionOptions) error {
	tm.operationMu.Lock()
	defer tm.operationMu.Unlock()
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if tm.isActive {
		return NewTransactionException("事务已在进行中")
	}
	if ctx == nil {
		return NewTransactionException("事务上下文不能为 nil")
	}
	if tm.db == nil || tm.db.DataSource == nil {
		return NewTransactionException("数据库连接未初始化")
	}

	// 每次事务都从默认值重新构建选项，避免继承上一次事务状态。
	effectiveOptions := TransactionOptions{
		Isolation: sql.LevelDefault,
		Timeout:   defaultTransactionTimeout,
	}
	if len(opts) > 0 {
		effectiveOptions.Isolation = opts[0].Isolation
		effectiveOptions.ReadOnly = opts[0].ReadOnly
		if opts[0].Timeout > 0 {
			effectiveOptions.Timeout = opts[0].Timeout
		}
	}

	// 创建事务选项
	txOptions := &sql.TxOptions{
		Isolation: effectiveOptions.Isolation,
		ReadOnly:  effectiveOptions.ReadOnly,
	}

	// 开始事务
	txCtx, cancel := context.WithTimeout(ctx, effectiveOptions.Timeout)

	tx, err := tm.db.DataSource.BeginTx(txCtx, txOptions)
	if err != nil {
		cancel()
		return NewTransactionExceptionWithCause(err, "开始事务失败")
	}

	tm.tx = tx
	tm.txCtx = txCtx
	tm.cancel = cancel
	tm.isActive = true
	tm.startTime = time.Now()
	tm.timeout = effectiveOptions.Timeout
	tm.isolation = effectiveOptions.Isolation
	tm.readOnly = effectiveOptions.ReadOnly
	tm.savepoints = make([]transactionSavepoint, 0)

	LogDebug("事务已开始，隔离级别: %v, 只读: %v", tm.isolation, tm.readOnly)
	return nil
}

// 提交事务
func (tm *TransactionManager) Commit() error {
	tm.operationMu.Lock()
	defer tm.operationMu.Unlock()
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if !tm.isActive {
		return NewTransactionException("没有活跃的事务")
	}

	err := tm.tx.Commit()
	duration := time.Since(tm.startTime)
	ctxErr := tm.transactionContextError()
	postCommitActions := append([]func(){}, tm.postCommitActions...)
	tm.reset()

	if err != nil {
		commitErr := error(NewTransactionExceptionWithCause(err, "提交事务失败"))
		if ctxErr != nil {
			return errors.Join(commitErr, ctxErr)
		}
		return commitErr
	}
	for _, action := range postCommitActions {
		action()
	}

	LogDebug("事务已提交，持续时间: %v", duration)
	return nil
}

// 回滚事务
func (tm *TransactionManager) Rollback() error {
	tm.operationMu.Lock()
	defer tm.operationMu.Unlock()
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if !tm.isActive {
		return NewTransactionException("没有活跃的事务")
	}

	err := tm.tx.Rollback()
	duration := time.Since(tm.startTime)
	ctxErr := tm.transactionContextError()
	tm.reset()

	if err != nil {
		rollbackErr := error(NewTransactionExceptionWithCause(err, "回滚事务失败"))
		if ctxErr != nil {
			return errors.Join(rollbackErr, ctxErr)
		}
		return rollbackErr
	}

	LogDebug("事务已回滚，持续时间: %v", duration)
	return nil
}

// 创建保存点
func (tm *TransactionManager) Savepoint(name string) error {
	tm.operationMu.Lock()
	defer tm.operationMu.Unlock()
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if !tm.isActive {
		return NewTransactionException("没有活跃的事务")
	}

	// 检查保存点是否已存在
	for _, sp := range tm.savepoints {
		if sp.name == name {
			return NewTransactionException("保存点已存在: " + name)
		}
	}

	_, err := tm.tx.Exec("SAVEPOINT " + name)
	if err != nil {
		return NewTransactionExceptionWithCause(err, "创建保存点失败: "+name)
	}

	tm.savepoints = append(tm.savepoints, transactionSavepoint{
		name:                  name,
		postCommitActionCount: len(tm.postCommitActions),
	})
	LogDebug("保存点已创建: %s", name)
	return nil
}

// 回滚到保存点
func (tm *TransactionManager) RollbackToSavepoint(name string) error {
	tm.operationMu.Lock()
	defer tm.operationMu.Unlock()
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if !tm.isActive {
		return NewTransactionException("没有活跃的事务")
	}

	// 检查保存点是否存在
	savepointIndex := -1
	for i, sp := range tm.savepoints {
		if sp.name == name {
			savepointIndex = i
			break
		}
	}

	if savepointIndex < 0 {
		return NewTransactionException("保存点不存在: " + name)
	}

	_, err := tm.tx.Exec("ROLLBACK TO SAVEPOINT " + name)
	if err != nil {
		return NewTransactionExceptionWithCause(err, "回滚到保存点失败: "+name)
	}

	// 数据库会撤销保存点之后的写入；同步丢弃对应的内存回填动作和后续保存点。
	actionCount := tm.savepoints[savepointIndex].postCommitActionCount
	if actionCount < len(tm.postCommitActions) {
		tm.postCommitActions = tm.postCommitActions[:actionCount]
	}
	tm.savepoints = tm.savepoints[:savepointIndex+1]

	LogDebug("已回滚到保存点: %s", name)
	return nil
}

// 释放保存点
func (tm *TransactionManager) ReleaseSavepoint(name string) error {
	tm.operationMu.Lock()
	defer tm.operationMu.Unlock()
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if !tm.isActive {
		return NewTransactionException("没有活跃的事务")
	}

	_, err := tm.tx.Exec("RELEASE SAVEPOINT " + name)
	if err != nil {
		return NewTransactionExceptionWithCause(err, "释放保存点失败: "+name)
	}

	// 从列表中移除保存点
	for i, sp := range tm.savepoints {
		if sp.name == name {
			tm.savepoints = append(tm.savepoints[:i], tm.savepoints[i+1:]...)
			break
		}
	}

	LogDebug("保存点已释放: %s", name)
	return nil
}

// 执行事务中的查询。
// 为保持 *sql.Rows 返回类型兼容，本方法只保护创建 Rows 的阶段；调用方必须在 Rows.Close
// 前避免与同一 TransactionManager 的 Repository、Exec、保存点或终态操作并发混用。
func (tm *TransactionManager) Query(query string, args ...any) (*sql.Rows, error) {
	tm.operationMu.Lock()
	defer tm.operationMu.Unlock()
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if !tm.isActive {
		return nil, NewTransactionException("没有活跃的事务")
	}

	return tm.tx.Query(query, args...)
}

// 执行事务中的查询（带上下文）。
// 为保持 *sql.Rows 返回类型兼容，本方法只保护创建 Rows 的阶段；调用方必须在 Rows.Close
// 前避免与同一 TransactionManager 的 Repository、Exec、保存点或终态操作并发混用。
func (tm *TransactionManager) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	tm.operationMu.Lock()
	defer tm.operationMu.Unlock()
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if !tm.isActive {
		return nil, NewTransactionException("没有活跃的事务")
	}

	return tm.tx.QueryContext(ctx, query, args...)
}

// 执行事务中的语句
func (tm *TransactionManager) Exec(query string, args ...any) (sql.Result, error) {
	tm.operationMu.Lock()
	defer tm.operationMu.Unlock()
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if !tm.isActive {
		return nil, NewTransactionException("没有活跃的事务")
	}

	return tm.tx.Exec(query, args...)
}

// 执行事务中的语句（带上下文）
func (tm *TransactionManager) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	tm.operationMu.Lock()
	defer tm.operationMu.Unlock()
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if !tm.isActive {
		return nil, NewTransactionException("没有活跃的事务")
	}

	return tm.tx.ExecContext(ctx, query, args...)
}

// 检查事务是否活跃
func (tm *TransactionManager) IsActive() bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.isActive
}

// 获取事务持续时间
func (tm *TransactionManager) GetDuration() time.Duration {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if !tm.isActive {
		return 0
	}

	return time.Since(tm.startTime)
}

// 获取保存点列表
func (tm *TransactionManager) GetSavepoints() []string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	result := make([]string, len(tm.savepoints))
	for i, savepoint := range tm.savepoints {
		result[i] = savepoint.name
	}
	return result
}

// 重置事务状态
func (tm *TransactionManager) reset() {
	cancel := tm.cancel

	tm.tx = nil
	tm.txCtx = nil
	tm.cancel = nil
	tm.isActive = false
	tm.startTime = time.Time{}
	tm.timeout = defaultTransactionTimeout
	tm.isolation = sql.LevelDefault
	tm.readOnly = false
	tm.savepoints = nil
	tm.postCommitActions = nil

	if cancel != nil {
		cancel()
	}
}

// transactionContextError 返回事务上下文当前的取消原因。
// 调用方必须在 reset 触发自身 cancel 之前获取该值。
func (tm *TransactionManager) transactionContextError() error {
	if tm.txCtx == nil {
		return nil
	}
	return tm.txCtx.Err()
}

// 使用事务执行函数（编程式事务）
func (tm *TransactionManager) ExecuteInTransaction(fn func(*TransactionManager) error, opts ...TransactionOptions) error {
	return tm.ExecuteInTransactionContext(context.Background(), fn, opts...)
}

// ExecuteInTransactionContext 使用调用方上下文执行事务函数。
func (tm *TransactionManager) ExecuteInTransactionContext(ctx context.Context, fn func(*TransactionManager) error, opts ...TransactionOptions) error {
	if fn == nil {
		return NewTransactionException("事务回调不能为 nil")
	}

	if err := tm.BeginContext(ctx, opts...); err != nil {
		return err
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			if tm.IsActive() {
				if rollbackErr := tm.Rollback(); rollbackErr != nil {
					LogError("事务回滚失败: %v", rollbackErr)
				}
			}
			panic(recovered)
		}
	}()

	callbackErr := fn(tm)
	if callbackErr != nil {
		rollbackErr := tm.Rollback()
		if rollbackErr != nil {
			return errors.Join(callbackErr, rollbackErr)
		}
		return callbackErr
	}

	return tm.Commit()
}

// 声明式事务装饰器
func WithTransaction(db *Db, fn func(*TransactionManager) error, opts ...TransactionOptions) error {
	return WithTransactionContext(context.Background(), db, fn, opts...)
}

// WithTransactionContext 使用调用方上下文执行声明式事务。
func WithTransactionContext(ctx context.Context, db *Db, fn func(*TransactionManager) error, opts ...TransactionOptions) error {
	tm := NewTransactionManager(db)
	return tm.ExecuteInTransactionContext(ctx, fn, opts...)
}
