package db233

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

/**
 * TransactionManager - 事务管理器
 *
 * 提供事务管理和分布式事务支持
 *
 * @author SolarisNeko
 * @since 2025-12-29
 */
type TransactionManager struct {
	db *Db
	tx *sql.Tx

	// 事务状态
	isActive  bool
	startTime time.Time
	timeout   time.Duration

	// 保存点管理
	savepoints []string

	// 锁
	mu sync.RWMutex

	// 事务选项
	isolation sql.IsolationLevel
	readOnly  bool
}

/**
 * TransactionOptions - 事务选项
 */
type TransactionOptions struct {
	Isolation sql.IsolationLevel
	ReadOnly  bool
	Timeout   time.Duration
}

/**
 * 创建事务管理器
 */
func NewTransactionManager(db *Db) *TransactionManager {
	return &TransactionManager{
		db:        db,
		timeout:   30 * time.Second, // 默认30秒超时
		isolation: sql.LevelDefault,
	}
}

/**
 * 开始事务
 */
func (tm *TransactionManager) Begin(opts ...TransactionOptions) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if tm.isActive {
		return NewTransactionException("事务已在进行中")
	}

	// 应用选项
	if len(opts) > 0 {
		opt := opts[0]
		if opt.Timeout > 0 {
			tm.timeout = opt.Timeout
		}
		tm.isolation = opt.Isolation
		tm.readOnly = opt.ReadOnly
	}

	// 创建事务选项
	txOptions := &sql.TxOptions{
		Isolation: tm.isolation,
		ReadOnly:  tm.readOnly,
	}

	// 开始事务
	ctx, cancel := context.WithTimeout(context.Background(), tm.timeout)
	defer cancel()

	tx, err := tm.db.DataSource.BeginTx(ctx, txOptions)
	if err != nil {
		return NewTransactionExceptionWithCause(err, "开始事务失败")
	}

	tm.tx = tx
	tm.isActive = true
	tm.startTime = time.Now()
	tm.savepoints = make([]string, 0)

	LogDebug("事务已开始，隔离级别: %v, 只读: %v", tm.isolation, tm.readOnly)
	return nil
}

/**
 * 提交事务
 */
func (tm *TransactionManager) Commit() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if !tm.isActive {
		return NewTransactionException("没有活跃的事务")
	}

	err := tm.tx.Commit()
	if err != nil {
		return NewTransactionExceptionWithCause(err, "提交事务失败")
	}

	duration := time.Since(tm.startTime)
	tm.reset()

	LogDebug("事务已提交，持续时间: %v", duration)
	return nil
}

/**
 * 回滚事务
 */
func (tm *TransactionManager) Rollback() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if !tm.isActive {
		return NewTransactionException("没有活跃的事务")
	}

	err := tm.tx.Rollback()
	if err != nil {
		return NewTransactionExceptionWithCause(err, "回滚事务失败")
	}

	duration := time.Since(tm.startTime)
	tm.reset()

	LogDebug("事务已回滚，持续时间: %v", duration)
	return nil
}

/**
 * 创建保存点
 */
func (tm *TransactionManager) Savepoint(name string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if !tm.isActive {
		return NewTransactionException("没有活跃的事务")
	}

	// 检查保存点是否已存在
	for _, sp := range tm.savepoints {
		if sp == name {
			return NewTransactionException("保存点已存在: " + name)
		}
	}

	_, err := tm.tx.Exec("SAVEPOINT " + name)
	if err != nil {
		return NewTransactionExceptionWithCause(err, "创建保存点失败: "+name)
	}

	tm.savepoints = append(tm.savepoints, name)
	LogDebug("保存点已创建: %s", name)
	return nil
}

/**
 * 回滚到保存点
 */
func (tm *TransactionManager) RollbackToSavepoint(name string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if !tm.isActive {
		return NewTransactionException("没有活跃的事务")
	}

	// 检查保存点是否存在
	found := false
	for _, sp := range tm.savepoints {
		if sp == name {
			found = true
			break
		}
	}

	if !found {
		return NewTransactionException("保存点不存在: " + name)
	}

	_, err := tm.tx.Exec("ROLLBACK TO SAVEPOINT " + name)
	if err != nil {
		return NewTransactionExceptionWithCause(err, "回滚到保存点失败: "+name)
	}

	LogDebug("已回滚到保存点: %s", name)
	return nil
}

/**
 * 释放保存点
 */
func (tm *TransactionManager) ReleaseSavepoint(name string) error {
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
		if sp == name {
			tm.savepoints = append(tm.savepoints[:i], tm.savepoints[i+1:]...)
			break
		}
	}

	LogDebug("保存点已释放: %s", name)
	return nil
}

/**
 * 执行事务中的查询
 */
func (tm *TransactionManager) Query(query string, args ...interface{}) (*sql.Rows, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if !tm.isActive {
		return nil, NewTransactionException("没有活跃的事务")
	}

	return tm.tx.Query(query, args...)
}

/**
 * 执行事务中的查询（带上下文）
 */
func (tm *TransactionManager) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if !tm.isActive {
		return nil, NewTransactionException("没有活跃的事务")
	}

	return tm.tx.QueryContext(ctx, query, args...)
}

/**
 * 执行事务中的语句
 */
func (tm *TransactionManager) Exec(query string, args ...interface{}) (sql.Result, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if !tm.isActive {
		return nil, NewTransactionException("没有活跃的事务")
	}

	return tm.tx.Exec(query, args...)
}

/**
 * 执行事务中的语句（带上下文）
 */
func (tm *TransactionManager) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if !tm.isActive {
		return nil, NewTransactionException("没有活跃的事务")
	}

	return tm.tx.ExecContext(ctx, query, args...)
}

/**
 * 检查事务是否活跃
 */
func (tm *TransactionManager) IsActive() bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.isActive
}

/**
 * 获取事务持续时间
 */
func (tm *TransactionManager) GetDuration() time.Duration {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if !tm.isActive {
		return 0
	}

	return time.Since(tm.startTime)
}

/**
 * 获取保存点列表
 */
func (tm *TransactionManager) GetSavepoints() []string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	result := make([]string, len(tm.savepoints))
	copy(result, tm.savepoints)
	return result
}

/**
 * 重置事务状态
 */
func (tm *TransactionManager) reset() {
	tm.tx = nil
	tm.isActive = false
	tm.startTime = time.Time{}
	tm.savepoints = nil
}

/**
 * 使用事务执行函数（编程式事务）
 */
func (tm *TransactionManager) ExecuteInTransaction(fn func(*TransactionManager) error, opts ...TransactionOptions) error {
	// 开始事务
	err := tm.Begin(opts...)
	if err != nil {
		return err
	}

	// 执行用户函数
	err = fn(tm)
	if err != nil {
		// 回滚事务
		rollbackErr := tm.Rollback()
		if rollbackErr != nil {
			LogError("事务回滚失败: %v", rollbackErr)
		}
		return err
	}

	// 提交事务
	return tm.Commit()
}

/**
 * 声明式事务装饰器
 */
func WithTransaction(db *Db, fn func(*TransactionManager) error, opts ...TransactionOptions) error {
	tm := NewTransactionManager(db)
	return tm.ExecuteInTransaction(fn, opts...)
}
