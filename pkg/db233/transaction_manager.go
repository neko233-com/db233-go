package db233

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const defaultTransactionTimeout = 30 * time.Second

const (
	transactionSavepointPrefix       = "db233_sp_"
	maxTransactionSavepointNameBytes = 63 - len(transactionSavepointPrefix)
)

type transactionSavepoint struct {
	name                         string
	sqlName                      string
	postCommitActionCount        int
	pendingAutoIncrementKeyCount int
}

// TransactionManager - 事务管理器
// 提供事务管理和分布式事务支持
type TransactionManager struct {
	db     *Db
	tx     *sql.Tx
	txCtx  context.Context
	cancel context.CancelFunc

	// generationRelease 在 BeginTx 前获取，并持续持有到事务 Commit/Rollback
	// 完成。这样清库轮换无法穿越一个仍可能提交旧数据的事务。
	databaseGeneration string
	generationRelease  func()
	txDoneStop         func() bool
	lastTerminalErr    error

	// operationMu 串行化同一事务上的 Repository 操作，并与终态操作互斥。
	operationMu       sync.Mutex
	postCommitActions []func()

	// pendingAutoIncrementKeys 防止同一零值自增实体在 Commit 回填前被重复 INSERT。
	pendingAutoIncrementKeys  map[uintptr]struct{}
	pendingAutoIncrementOrder []uintptr
	autoIncrementStep         int64
	autoIncrementStepLoaded   bool

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
	// 新事务取代上一次已自动回滚但尚未被显式终态调用读取的原因。
	tm.lastTerminalErr = nil
	if ctx == nil {
		return NewTransactionException("事务上下文不能为 nil")
	}
	if ctxErr := contextCauseError(ctx); ctxErr != nil {
		return NewTransactionExceptionWithCause(ctxErr, "事务上下文已结束")
	}
	if tm.db == nil || tm.db.DataSource == nil {
		return NewTransactionException("数据库连接未初始化")
	}
	databaseGeneration, releaseGeneration, generationErr := tm.db.lockCurrentDatabaseGeneration()
	if generationErr != nil {
		return NewTransactionExceptionWithCause(generationErr, "数据库 generation 暂不可写")
	}
	generationLeaseTransferred := false
	defer func() {
		if !generationLeaseTransferred {
			releaseGeneration()
		}
	}()

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
		beginErr := joinErrorWithContext(err, txCtx)
		cancel()
		return NewTransactionExceptionWithCause(beginErr, "开始事务失败")
	}

	tm.tx = tx
	tm.txCtx = txCtx
	tm.cancel = cancel
	tm.databaseGeneration = databaseGeneration
	tm.generationRelease = releaseGeneration
	generationLeaseTransferred = true
	tm.isActive = true
	tm.startTime = time.Now()
	tm.timeout = effectiveOptions.Timeout
	tm.isolation = effectiveOptions.Isolation
	tm.readOnly = effectiveOptions.ReadOnly
	tm.savepoints = make([]transactionSavepoint, 0)
	activeTx := tx
	activeCtx := txCtx
	tm.txDoneStop = context.AfterFunc(txCtx, func() {
		tm.finalizeCanceledTransaction(activeTx, activeCtx)
	})

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
		if tm.lastTerminalErr != nil {
			terminalErr := tm.lastTerminalErr
			tm.lastTerminalErr = nil
			return NewTransactionExceptionWithCause(terminalErr, "事务已因上下文结束而自动回滚")
		}
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
		if tm.lastTerminalErr != nil {
			terminalErr := tm.lastTerminalErr
			tm.lastTerminalErr = nil
			return NewTransactionExceptionWithCause(terminalErr, "事务已因上下文结束而自动回滚")
		}
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
	return tm.SavepointContext(context.Background(), name)
}

// SavepointContext 创建保存点。逻辑名称使用跨数据库保守字符集，并映射到内部 SQL 标识符。
func (tm *TransactionManager) SavepointContext(ctx context.Context, name string) error {
	canonicalName, nameErr := canonicalTransactionSavepointName(name)
	if nameErr != nil {
		return nameErr
	}
	if ctx == nil {
		return NewTransactionException("保存点上下文不能为 nil")
	}
	tm.operationMu.Lock()
	defer tm.operationMu.Unlock()
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if !tm.isActive {
		return NewTransactionException("没有活跃的事务")
	}

	// 检查保存点是否已存在
	for _, sp := range tm.savepoints {
		if sp.name == canonicalName {
			return NewTransactionException("保存点已存在: " + canonicalName)
		}
	}

	operationCtx, cleanup, _, ctxErr := mergeTransactionOperationContext(ctx, tm.txCtx)
	if ctxErr != nil {
		return NewTransactionExceptionWithCause(ctxErr, "创建保存点时上下文已结束: "+canonicalName)
	}
	defer cleanup()

	sqlName := transactionSavepointPrefix + canonicalName
	_, err := tm.tx.ExecContext(operationCtx, "SAVEPOINT "+sqlName)
	if err != nil {
		return NewTransactionExceptionWithCause(
			joinErrorWithContext(err, operationCtx),
			"创建保存点失败: "+canonicalName,
		)
	}

	tm.savepoints = append(tm.savepoints, transactionSavepoint{
		name:                         canonicalName,
		sqlName:                      sqlName,
		postCommitActionCount:        len(tm.postCommitActions),
		pendingAutoIncrementKeyCount: len(tm.pendingAutoIncrementOrder),
	})
	LogDebug("保存点已创建: %s", canonicalName)
	return nil
}

// 回滚到保存点
func (tm *TransactionManager) RollbackToSavepoint(name string) error {
	return tm.RollbackToSavepointContext(context.Background(), name)
}

// RollbackToSavepointContext 使用调用方上下文回滚到保存点。
func (tm *TransactionManager) RollbackToSavepointContext(ctx context.Context, name string) error {
	canonicalName, nameErr := canonicalTransactionSavepointName(name)
	if nameErr != nil {
		return nameErr
	}
	if ctx == nil {
		return NewTransactionException("保存点上下文不能为 nil")
	}
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
		if sp.name == canonicalName {
			savepointIndex = i
			break
		}
	}

	if savepointIndex < 0 {
		return NewTransactionException("保存点不存在: " + canonicalName)
	}

	operationCtx, cleanup, _, ctxErr := mergeTransactionOperationContext(ctx, tm.txCtx)
	if ctxErr != nil {
		return NewTransactionExceptionWithCause(ctxErr, "回滚保存点时上下文已结束: "+canonicalName)
	}
	defer cleanup()

	savepoint := tm.savepoints[savepointIndex]
	_, err := tm.tx.ExecContext(operationCtx, "ROLLBACK TO SAVEPOINT "+savepoint.sqlName)
	if err != nil {
		return NewTransactionExceptionWithCause(
			joinErrorWithContext(err, operationCtx),
			"回滚到保存点失败: "+canonicalName,
		)
	}

	// 数据库会撤销保存点之后的写入；同步丢弃对应的内存回填动作和后续保存点。
	actionCount := savepoint.postCommitActionCount
	if actionCount < len(tm.postCommitActions) {
		tm.postCommitActions = tm.postCommitActions[:actionCount]
	}
	tm.truncatePendingAutoIncrementKeys(savepoint.pendingAutoIncrementKeyCount)
	tm.savepoints = tm.savepoints[:savepointIndex+1]

	LogDebug("已回滚到保存点: %s", canonicalName)
	return nil
}

// 释放保存点
func (tm *TransactionManager) ReleaseSavepoint(name string) error {
	return tm.ReleaseSavepointContext(context.Background(), name)
}

// ReleaseSavepointContext 使用调用方上下文释放保存点。
func (tm *TransactionManager) ReleaseSavepointContext(ctx context.Context, name string) error {
	canonicalName, nameErr := canonicalTransactionSavepointName(name)
	if nameErr != nil {
		return nameErr
	}
	if ctx == nil {
		return NewTransactionException("保存点上下文不能为 nil")
	}
	tm.operationMu.Lock()
	defer tm.operationMu.Unlock()
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if !tm.isActive {
		return NewTransactionException("没有活跃的事务")
	}

	savepointIndex := -1
	for i, sp := range tm.savepoints {
		if sp.name == canonicalName {
			savepointIndex = i
			break
		}
	}
	if savepointIndex < 0 {
		return NewTransactionException("保存点不存在: " + canonicalName)
	}

	operationCtx, cleanup, _, ctxErr := mergeTransactionOperationContext(ctx, tm.txCtx)
	if ctxErr != nil {
		return NewTransactionExceptionWithCause(ctxErr, "释放保存点时上下文已结束: "+canonicalName)
	}
	defer cleanup()

	_, err := tm.tx.ExecContext(operationCtx, "RELEASE SAVEPOINT "+tm.savepoints[savepointIndex].sqlName)
	if err != nil {
		return NewTransactionExceptionWithCause(
			joinErrorWithContext(err, operationCtx),
			"释放保存点失败: "+canonicalName,
		)
	}

	// MySQL 只删除命名保存点；PostgreSQL/SQL 标准同时删除其后建立的保存点。
	if tm.db != nil && tm.db.DatabaseType == EnumDatabaseTypePostgreSQL {
		tm.savepoints = tm.savepoints[:savepointIndex]
	} else {
		tm.savepoints = append(tm.savepoints[:savepointIndex], tm.savepoints[savepointIndex+1:]...)
	}

	LogDebug("保存点已释放: %s", canonicalName)
	return nil
}

// 执行事务中的查询。
// 为保持 *sql.Rows 返回类型兼容，本方法只保护创建 Rows 的阶段；调用方必须在 Rows.Close
// 前避免与同一 TransactionManager 的 Repository、Exec、保存点或终态操作并发混用。
func (tm *TransactionManager) Query(query string, args ...any) (*sql.Rows, error) {
	return tm.QueryContext(context.Background(), query, args...)
}

// QueryContext 执行事务查询；查询启动同时受调用方 context 与事务 context 约束。
func (tm *TransactionManager) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if ctx == nil {
		return nil, NewTransactionException("查询上下文不能为 nil")
	}
	tm.operationMu.Lock()
	defer tm.operationMu.Unlock()
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if !tm.isActive {
		return nil, NewTransactionException("没有活跃的事务")
	}

	operationCtx, cleanup, detach, ctxErr := mergeTransactionOperationContext(ctx, tm.txCtx)
	if ctxErr != nil {
		return nil, NewTransactionExceptionWithCause(ctxErr, "事务查询上下文已结束")
	}
	rows, err := tm.tx.QueryContext(operationCtx, query, args...)
	if err != nil {
		joinedErr := joinErrorWithContext(err, operationCtx)
		cleanup()
		return nil, NewQueryExceptionWithCause(joinedErr, "事务查询失败: "+sqlForError(query))
	}
	// *sql.Rows 仍由 database/sql 自身同时监听 query ctx 与 transaction ctx。
	// 停止额外 bridge，避免每个开放 Rows 留下 transaction AfterFunc。
	detach()
	return rows, nil
}

// 执行事务中的语句
func (tm *TransactionManager) Exec(query string, args ...any) (sql.Result, error) {
	return tm.ExecContext(context.Background(), query, args...)
}

// ExecContext 执行事务语句；执行同时受调用方 context 与事务 context 约束。
func (tm *TransactionManager) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if ctx == nil {
		return nil, NewTransactionException("执行上下文不能为 nil")
	}
	tm.operationMu.Lock()
	defer tm.operationMu.Unlock()
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if !tm.isActive {
		return nil, NewTransactionException("没有活跃的事务")
	}
	if tm.db != nil && tm.db.DatabaseType == EnumDatabaseTypeMySQL && mysqlMigrationHasImplicitCommit(query) {
		return nil, NewTransactionException(
			"MySQL DDL/管理语句可能隐式提交，禁止通过 TransactionManager.Exec 执行；请使用迁移 API",
		)
	}

	operationCtx, cleanup, _, ctxErr := mergeTransactionOperationContext(ctx, tm.txCtx)
	if ctxErr != nil {
		return nil, NewTransactionExceptionWithCause(ctxErr, "事务执行上下文已结束")
	}
	defer cleanup()
	result, err := tm.tx.ExecContext(operationCtx, query, args...)
	if err != nil {
		return nil, NewQueryExceptionWithCause(joinErrorWithContext(err, operationCtx), "事务执行失败: "+sqlForError(query))
	}
	return result, nil
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
	releaseGeneration := tm.generationRelease
	stopDoneCallback := tm.txDoneStop

	tm.tx = nil
	tm.txCtx = nil
	tm.cancel = nil
	tm.databaseGeneration = ""
	tm.generationRelease = nil
	tm.txDoneStop = nil
	tm.isActive = false
	tm.startTime = time.Time{}
	tm.timeout = defaultTransactionTimeout
	tm.isolation = sql.LevelDefault
	tm.readOnly = false
	tm.savepoints = nil
	tm.postCommitActions = nil
	tm.pendingAutoIncrementKeys = nil
	tm.pendingAutoIncrementOrder = nil
	tm.autoIncrementStep = 0
	tm.autoIncrementStepLoaded = false

	if stopDoneCallback != nil {
		stopDoneCallback()
	}
	if cancel != nil {
		cancel()
	}
	if releaseGeneration != nil {
		releaseGeneration()
	}
}

// finalizeCanceledTransaction mirrors database/sql's automatic rollback in the
// manager state and, crucially, releases the generation lease even when callers
// omit an explicit Rollback after a deadline/cancellation.
func (tm *TransactionManager) finalizeCanceledTransaction(tx *sql.Tx, txCtx context.Context) {
	if tm == nil || tx == nil {
		return
	}
	tm.operationMu.Lock()
	defer tm.operationMu.Unlock()
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if !tm.isActive || tm.tx != tx || tm.txCtx != txCtx {
		return
	}
	terminalErr := contextCauseError(txCtx)
	if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
		terminalErr = errors.Join(terminalErr, NewTransactionExceptionWithCause(rollbackErr, "取消事务自动回滚失败"))
	}
	tm.reset()
	tm.lastTerminalErr = terminalErr
}

// transactionContextError 返回事务上下文当前的取消原因。
// 调用方必须在 reset 触发自身 cancel 之前获取该值。
func (tm *TransactionManager) transactionContextError() error {
	if tm.txCtx == nil {
		return nil
	}
	return contextCauseError(tm.txCtx)
}

func (tm *TransactionManager) truncatePendingAutoIncrementKeys(count int) {
	if count < 0 {
		count = 0
	}
	if count >= len(tm.pendingAutoIncrementOrder) {
		return
	}
	for _, key := range tm.pendingAutoIncrementOrder[count:] {
		delete(tm.pendingAutoIncrementKeys, key)
	}
	tm.pendingAutoIncrementOrder = tm.pendingAutoIncrementOrder[:count]
}

func canonicalTransactionSavepointName(name string) (string, error) {
	if name == "" {
		return "", NewTransactionException("保存点名称不能为空")
	}
	if len(name) > maxTransactionSavepointNameBytes {
		return "", NewTransactionException(fmt.Sprintf(
			"保存点名称过长: 最大 %d 字节",
			maxTransactionSavepointNameBytes,
		))
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		valid := ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || i > 0 && ch >= '0' && ch <= '9'
		if !valid {
			return "", NewTransactionException("保存点名称仅允许 ASCII 字母、数字和下划线，且不能以数字开头")
		}
	}
	return strings.ToLower(name), nil
}

// mergeTransactionOperationContext 构造同时受操作 context 和事务 context 约束的 context。
// cleanup 用于无 Rows 操作；detach 用于成功返回 *sql.Rows 时移除额外 bridge。
func mergeTransactionOperationContext(
	operationCtx context.Context,
	transactionCtx context.Context,
) (merged context.Context, cleanup func(), detach func(), err error) {
	if operationCtx == nil {
		return nil, nil, nil, NewTransactionException("操作上下文不能为 nil")
	}
	if operationErr := contextCauseError(operationCtx); operationErr != nil {
		return nil, nil, nil, operationErr
	}
	if transactionCtx == nil {
		return nil, nil, nil, NewTransactionException("事务上下文未初始化")
	}
	if transactionErr := contextCauseError(transactionCtx); transactionErr != nil {
		return nil, nil, nil, transactionErr
	}

	mergedCtx, cancel := context.WithCancelCause(operationCtx)
	stop := context.AfterFunc(transactionCtx, func() {
		cancel(context.Cause(transactionCtx))
	})
	cleanupFn := func() {
		stop()
		cancel(context.Canceled)
	}
	detachFn := func() {
		stop()
	}
	return mergedCtx, cleanupFn, detachFn, nil
}

func contextCauseError(ctx context.Context) error {
	if ctx == nil {
		return NewTransactionException("context 不能为 nil")
	}
	ctxErr := ctx.Err()
	if ctxErr == nil {
		return nil
	}
	cause := context.Cause(ctx)
	if cause == nil || errors.Is(ctxErr, cause) {
		return ctxErr
	}
	return errors.Join(ctxErr, cause)
}

func joinErrorWithContext(err error, ctx context.Context) error {
	if err == nil {
		return nil
	}
	ctxErr := contextCauseError(ctx)
	if ctxErr == nil {
		return err
	}
	return errors.Join(err, ctxErr)
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
					LogError("事务回滚失败: %s", safeErrorForLog(rollbackErr))
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
