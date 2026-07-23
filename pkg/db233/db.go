package db233

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
)

// =====================================================
// API 接口定义
// =====================================================

// DbApi 定义数据库操作的统一抽象
// 提供三层操作接口：底层 SQL、ORM、和便利方法
type DbApi interface {
	// 获取底层数据源
	GetDataSource() *sql.DB

	// ========== 最底层 Native SQL 接口 ==========
	// ExecuteSqlByStatement 最底层：执行原生 SQL 并返回原始行数据
	ExecuteSqlByStatement(statement *SqlStatement) []map[string]any

	// ========== ORM 接口 ==========
	// Query ORM 快捷查询
	Query(sql string, params ...any) []map[string]any
	// Save 保存实体
	Save(entity any) error
	// Update 更新实体
	Update(entity any) error
	// Delete 删除实体
	Delete(entity any) error
	// FindById 按 ID 查询
	FindById(id any, entity any) error

	// ========== 对外原生 SQL 接口 ==========
	// ExecuteQuery 执行占位符 SQL 并使用批量参数进行多组查询，返回映射后的结果列表（向后兼容）
	ExecuteQuery(query string, paramsArray [][]any, returnType any) []any
	// ExecuteQueryByStatement 使用 SqlStatement 执行查询并返回 ORM 映射结果
	ExecuteQueryByStatement(statement *SqlStatement) []map[string]any
	// ExecuteUpdateByStatement 使用 SqlStatement 执行更新语句，返回影响行数
	ExecuteUpdateByStatement(statement *SqlStatement) int
	// ExecuteUpdateMultiRows 使用 SQL 与多行参数执行批量更新，返回总影响行数
	ExecuteUpdateMultiRows(query string, multiRowParams [][]any) int
	// ExecuteUpdateMultiRowsNamed 使用 SQL 与多行命名参数执行批量更新，返回总影响行数
	ExecuteUpdateMultiRowsNamed(sql string, paramsList []map[string]any) int
	// ExecuteUpdateNamed 使用 SQL 与命名参数执行更新语句，返回影响行数
	ExecuteUpdateNamed(sql string, params map[string]any) (int64, error)
	// ExecuteWithConnection 提供对底层 *sql.Conn 的回调
	ExecuteWithConnection(fn func(*sql.Conn) error) error
}

// StrictQueryer 描述 all-or-error 的严格 ORM 查询能力，不改变 DbApi 的兼容契约。
type StrictQueryer interface {
	ExecuteQueryStrictContext(
		ctx context.Context,
		query string,
		paramsArray [][]any,
		returnType any,
	) ([]any, error)
}

// Db 是数据库操作核心类型，封装了数据源、数据库分组、容错管理器等信息。
// Db 对象负责执行 SQL、管理容错逻辑与辅助方法。
type Db struct {
	DataSource   *sql.DB
	DbId         int
	DbGroup      *DbGroup
	DatabaseType EnumDatabaseType // 数据库类型，默认为 MySQL
	// FaultTolerantMgr 容错管理器（可选）
	FaultTolerantMgr *FaultTolerantManager
	// WriteJournal 本地 WAL（可选，游戏服数据不丢）
	WriteJournal *LocalWriteJournal
	// SessionRepo Session 仓储（InitGameDb 创建）
	SessionRepo *SessionRepository

	resourceMu            sync.Mutex
	closing               bool
	closingState          atomic.Bool
	bufferedRepositories  *sync.Map // *BaseCrudRepository -> struct{}; 指针保持 Db 值可比较的 v1 API 特性
	generationMu          sync.RWMutex
	rotationMu            sync.Mutex
	databaseGeneration    string
	generationErr         error
	generationUnavailable atomic.Bool
	flushWriteMetrics     flushWriteMetricsCollector
	entitySchemaVersions  *entitySchemaVersionState
	closeOnce             sync.Once
	closeErr              error
}

// closeRowsForCompatibility closes rows owned by legacy no-error APIs. New
// code should use strict methods that return Rows.Err and Close errors.
func closeRowsForCompatibility(rows *sql.Rows) {
	if rows == nil {
		return
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		LogError("兼容查询行遍历失败: %s", safeErrorForLog(rowsErr))
	}
	if closeErr := rows.Close(); closeErr != nil {
		LogError("关闭兼容查询结果集失败: %s", safeErrorForLog(closeErr))
	}
}

func (db *Db) configureDatabaseGeneration(generation string) error {
	if db == nil {
		return nil
	}
	db.generationMu.Lock()
	defer db.generationMu.Unlock()
	if db.databaseGeneration != "" && generation != "" && db.databaseGeneration != generation {
		return fmt.Errorf("DatabaseGeneration 已配置: %s", safeValueForLog(db.databaseGeneration))
	}
	db.databaseGeneration = generation
	db.generationErr = nil
	db.generationUnavailable.Store(false)
	return nil
}

func (db *Db) databaseGenerationSnapshot() string {
	if db == nil {
		return ""
	}
	db.generationMu.RLock()
	defer db.generationMu.RUnlock()
	return db.databaseGeneration
}

// lockDatabaseGeneration 阻止运行时 generation 轮换穿越当前操作。
func (db *Db) lockDatabaseGeneration(expected string) (string, func(), error) {
	if db == nil {
		return "", func() {}, nil
	}
	// 轮换先发布 unavailable，再等待既有读租约排空。前后两次检查
	// 同时覆盖“检查后、加锁前”与“等待写锁期间”两个竞态窗口。
	if db.generationUnavailable.Load() {
		return "", func() {}, ErrDatabaseGenerationBlocked
	}
	db.generationMu.RLock()
	if db.generationUnavailable.Load() {
		db.generationMu.RUnlock()
		return "", func() {}, ErrDatabaseGenerationBlocked
	}
	if db.generationErr != nil {
		err := db.generationErr
		db.generationMu.RUnlock()
		return "", func() {}, err
	}
	current := db.databaseGeneration
	if expected != current {
		db.generationMu.RUnlock()
		return current, func() {}, fmt.Errorf(
			"%w: 对象=%s, 当前=%s",
			ErrDatabaseGenerationChanged,
			safeValueForLog(expected),
			safeValueForLog(current),
		)
	}
	return current, db.generationMu.RUnlock, nil
}

func (db *Db) lockCurrentDatabaseGeneration() (string, func(), error) {
	if db == nil {
		return "", func() {}, nil
	}
	if db.generationUnavailable.Load() {
		return "", func() {}, ErrDatabaseGenerationBlocked
	}
	db.generationMu.RLock()
	if db.generationUnavailable.Load() {
		db.generationMu.RUnlock()
		return "", func() {}, ErrDatabaseGenerationBlocked
	}
	if db.generationErr != nil {
		err := db.generationErr
		db.generationMu.RUnlock()
		return "", func() {}, err
	}
	return db.databaseGeneration, db.generationMu.RUnlock, nil
}

// DatabaseGeneration 返回当前数据库逻辑代次。
func (db *Db) DatabaseGeneration() string {
	return db.databaseGenerationSnapshot()
}

func (db *Db) isDatabaseGenerationUnavailable() bool {
	return db != nil && db.generationUnavailable.Load()
}

// DatabaseGenerationTransition 是跨清库事务的独占 generation 屏障。
// Begin 成功后必须调用 Commit 或 Abort；不可复制。
type DatabaseGenerationTransition struct {
	db                     *Db
	newGeneration          string
	previousGeneration     string
	previousErr            error
	previousUnavailable    bool
	sessionRepo            *SessionRepository
	writeJournal           *LocalWriteJournal
	faultTolerantManager   *FaultTolerantManager
	bufferedRepositories   *sync.Map
	sessionAdmissionPaused bool
	sessionLocked          bool
	generationLocked       bool
	mu                     sync.Mutex
	finalized              bool
}

// BeginDatabaseGenerationTransition 在清库事务开始前暂停并排空 Session、写缓冲、WAL 回放和失败重试。
// 调用方持有返回 token 执行“清库 + 新 generation”同一事务；提交后调用 Commit，回滚后调用 Abort。
func (db *Db) BeginDatabaseGenerationTransition(generation string) (*DatabaseGenerationTransition, error) {
	if db == nil {
		return nil, NewValidationException("Db 不能为 nil")
	}
	if generation == "" {
		return nil, NewValidationException("DatabaseGeneration 不能为空")
	}
	db.rotationMu.Lock()
	db.resourceMu.Lock()
	closing := db.closing
	previousUnavailable := db.generationUnavailable.Load()
	if !closing {
		// 在与 Repository 注册相同的临界区发布屏障，禁止新
		// WriteBuffer 穿越“资源快照已取、unavailable 尚未可见”的窗口。
		db.generationUnavailable.Store(true)
	}
	sessionRepo := db.SessionRepo
	writeJournal := db.WriteJournal
	faultTolerantManager := db.FaultTolerantMgr
	bufferedRepositories := db.bufferedRepositoryRegistryLocked()
	db.resourceMu.Unlock()
	if closing {
		db.rotationMu.Unlock()
		return nil, ErrCrudRepositoryClosed
	}

	transition := &DatabaseGenerationTransition{
		db:                   db,
		newGeneration:        generation,
		previousUnavailable:  previousUnavailable,
		sessionRepo:          sessionRepo,
		writeJournal:         writeJournal,
		faultTolerantManager: faultTolerantManager,
		bufferedRepositories: bufferedRepositories,
	}
	if sessionRepo != nil {
		if err := sessionRepo.pauseAdmissionAndWait(); err != nil {
			db.generationUnavailable.Store(transition.previousUnavailable)
			db.rotationMu.Unlock()
			return nil, fmt.Errorf("暂停 Session 写入: %w", err)
		}
		transition.sessionAdmissionPaused = true
		sessionRepo.generationMu.Lock()
		transition.sessionLocked = true
	}
	db.generationMu.Lock()
	transition.generationLocked = true
	transition.previousGeneration = db.databaseGeneration
	transition.previousErr = db.generationErr
	if db.databaseGeneration == generation && db.generationErr == nil {
		return transition.failBegin(NewValidationException("新 DatabaseGeneration 必须不同于当前代次"))
	}

	// generationErr 非空只会来自一个已签发 token 的 Commit/FailClosed；签发前
	// 已完成下述严格排空。此时显式 Rotate 是运维确认后的恢复重试，直接重试
	// 组件轮换，避免组件自身的 fail-closed 状态反过来阻断恢复。
	if transition.previousErr == nil {
		// unavailable + Session admission gate 阻断所有新写；两个 generation 写锁已
		// 等完直接写、事务、Session/WriteBuffer 的旧读租约。下面只调用不重入
		// generation RLock 的内部严格路径，直到旧代所有待写数据都已持久化。
		if sessionRepo != nil {
			if err := sessionRepo.flushAllForGenerationTransitionUnderLease(transition.previousGeneration); err != nil {
				return transition.failBegin(err)
			}
		}

		var writeBufferErr error
		transition.bufferedRepositories.Range(func(key, _ any) bool {
			repository, ok := key.(*BaseCrudRepository)
			if !ok || repository == nil {
				return true
			}
			if err := repository.flushWriteBufferUnderGenerationLease(transition.previousGeneration); err != nil {
				writeBufferErr = fmt.Errorf("刷新旧 generation 写缓冲: %w", err)
				return false
			}
			return true
		})
		if writeBufferErr != nil {
			return transition.failBegin(writeBufferErr)
		}

		if writeJournal != nil {
			remaining, err := writeJournal.drainUnderGenerationLeaseStrict(transition.previousGeneration)
			if err != nil || remaining != 0 {
				if err == nil {
					err = NewQueryException(fmt.Sprintf("WAL 排空后仍有 %d 条待回放", remaining))
				}
				return transition.failBegin(fmt.Errorf("排空旧 generation WAL: %w", err))
			}
		}
		if faultTolerantManager != nil {
			remaining, err := faultTolerantManager.drainUnderGenerationLeaseStrict(transition.previousGeneration)
			if err != nil || remaining != 0 {
				if err == nil {
					err = NewQueryException(fmt.Sprintf("失败操作排空后仍有 %d 条待重试", remaining))
				}
				return transition.failBegin(fmt.Errorf("排空旧 generation 失败操作: %w", err))
			}
		}
	}

	// 仅在所有旧代写入已确认落库后发布 blocked 状态并签发 token。
	db.generationErr = ErrDatabaseGenerationBlocked
	return transition, nil
}

func (transition *DatabaseGenerationTransition) failBegin(cause error) (*DatabaseGenerationTransition, error) {
	db := transition.db
	db.databaseGeneration = transition.previousGeneration
	db.generationErr = transition.previousErr
	db.generationUnavailable.Store(transition.previousUnavailable)
	transition.releaseLocks(false)
	return nil, cause
}

// waitForExclusiveLockDrain waits until every operation admitted before the
// generation barrier has left the supplied lock hierarchy. Locks are acquired
// in caller-specified order and released in reverse order.
func waitForExclusiveLockDrain(locks ...sync.Locker) {
	for _, lock := range locks {
		lock.Lock()
	}
	for index := len(locks) - 1; index >= 0; index-- {
		locks[index].Unlock()
	}
}

// Commit 在清库事务提交后隔离旧恢复数据并切换 generation。
func (transition *DatabaseGenerationTransition) Commit() error {
	if transition == nil || transition.db == nil {
		return NewValidationException("DatabaseGenerationTransition 不能为空")
	}
	transition.mu.Lock()
	defer transition.mu.Unlock()
	if transition.finalized {
		return NewValidationException("DatabaseGenerationTransition 已结束")
	}
	db := transition.db

	var rotationErrors []error
	if transition.sessionRepo != nil {
		transition.sessionRepo.rotateDatabaseGenerationLocked(transition.newGeneration)
	}
	transition.bufferedRepositories.Range(func(key, _ any) bool {
		repository, ok := key.(*BaseCrudRepository)
		if ok && repository != nil {
			repository.rotateWriteBufferDatabaseGeneration(transition.newGeneration)
		}
		return true
	})
	if transition.writeJournal != nil {
		if err := transition.writeJournal.rotateDatabaseGenerationUnderBarrier(transition.newGeneration); err != nil {
			rotationErrors = append(rotationErrors, fmt.Errorf("轮换 WAL generation: %w", err))
		}
	}
	if transition.faultTolerantManager != nil {
		if err := transition.faultTolerantManager.rotateDatabaseGenerationUnderBarrier(transition.newGeneration); err != nil {
			rotationErrors = append(rotationErrors, fmt.Errorf("轮换失败操作 generation: %w", err))
		}
	}
	if len(rotationErrors) > 0 {
		db.generationErr = fmt.Errorf("%w: %w", ErrDatabaseGenerationBlocked, errors.Join(rotationErrors...))
		transition.releaseLocks(true)
		return db.generationErr
	}
	db.databaseGeneration = transition.newGeneration
	db.generationErr = nil
	db.generationUnavailable.Store(false)
	transition.releaseLocks(true)
	return nil
}

// Abort 在清库事务回滚后恢复旧 generation 与旧恢复队列。
func (transition *DatabaseGenerationTransition) Abort() error {
	if transition == nil || transition.db == nil {
		return NewValidationException("DatabaseGenerationTransition 不能为空")
	}
	transition.mu.Lock()
	defer transition.mu.Unlock()
	if transition.finalized {
		return NewValidationException("DatabaseGenerationTransition 已结束")
	}
	db := transition.db
	db.databaseGeneration = transition.previousGeneration
	db.generationErr = transition.previousErr
	db.generationUnavailable.Store(transition.previousUnavailable)
	transition.releaseLocks(true)
	return nil
}

// FailClosed 在清库事务结果未知或回滚失败时结束屏障并保持数据库永久拒写。
// 调用后只能在确认数据库实际 generation 后显式 RotateDatabaseGeneration，或重启进程恢复。
func (transition *DatabaseGenerationTransition) FailClosed(cause error) error {
	if transition == nil || transition.db == nil {
		return NewValidationException("DatabaseGenerationTransition 不能为空")
	}
	transition.mu.Lock()
	defer transition.mu.Unlock()
	if transition.finalized {
		return NewValidationException("DatabaseGenerationTransition 已结束")
	}
	db := transition.db
	if cause == nil {
		cause = errors.New("数据库 generation 切换结果未知")
	}
	db.databaseGeneration = transition.previousGeneration
	db.generationErr = fmt.Errorf("%w: %w", ErrDatabaseGenerationBlocked, cause)
	db.generationUnavailable.Store(true)
	transition.releaseLocks(true)
	return db.generationErr
}

func (transition *DatabaseGenerationTransition) releaseLocks(finalize bool) {
	db := transition.db
	if finalize {
		transition.finalized = true
	}
	if transition.generationLocked {
		db.generationMu.Unlock()
		transition.generationLocked = false
	}
	if transition.sessionLocked {
		transition.sessionRepo.generationMu.Unlock()
		transition.sessionLocked = false
	}
	if transition.sessionAdmissionPaused {
		transition.sessionRepo.resumeAdmission()
		transition.sessionAdmissionPaused = false
	}
	db.rotationMu.Unlock()
}

// RotateDatabaseGeneration 是无并发清库事务时的便利入口。
// 有清库事务时必须使用 BeginDatabaseGenerationTransition，并在事务提交后 Commit。
func (db *Db) RotateDatabaseGeneration(generation string) error {
	if db == nil {
		return nil
	}
	db.generationMu.RLock()
	alreadyCurrent := db.databaseGeneration == generation && db.generationErr == nil
	db.generationMu.RUnlock()
	if alreadyCurrent {
		return nil
	}
	transition, err := db.BeginDatabaseGenerationTransition(generation)
	if err != nil {
		return err
	}
	return transition.Commit()
}

// NewDb 创建一个默认使用 MySQL 的 Db 实例。
func NewDb(dataSource *sql.DB, dbId int, dbGroup *DbGroup) *Db {
	return &Db{
		DataSource:           dataSource,
		DbId:                 dbId,
		DbGroup:              dbGroup,
		DatabaseType:         EnumDatabaseTypeMySQL, // 默认 MySQL
		bufferedRepositories: &sync.Map{},
		entitySchemaVersions: &entitySchemaVersionState{},
	}
}

// NewDbWithType 创建一个带指定数据库类型的 Db 实例。
func NewDbWithType(dataSource *sql.DB, dbId int, dbGroup *DbGroup, dbType EnumDatabaseType) *Db {
	if dbType == "" || !dbType.IsValid() {
		dbType = EnumDatabaseTypeMySQL
	}
	return &Db{
		DataSource:           dataSource,
		DbId:                 dbId,
		DbGroup:              dbGroup,
		DatabaseType:         dbType,
		bufferedRepositories: &sync.Map{},
		entitySchemaVersions: &entitySchemaVersionState{},
	}
}

// bufferedRepositoryRegistryLocked 要求 resourceMu 已持有。支持历史上
// 直接构造 Db{} 的调用方，且指针一旦发布永不替换。
func (db *Db) bufferedRepositoryRegistryLocked() *sync.Map {
	if db.bufferedRepositories == nil {
		db.bufferedRepositories = &sync.Map{}
	}
	return db.bufferedRepositories
}

func (db *Db) registerBufferedRepository(repository *BaseCrudRepository) bool {
	if db == nil || repository == nil {
		return false
	}
	db.resourceMu.Lock()
	defer db.resourceMu.Unlock()
	if db.closing {
		return false
	}
	if db.generationUnavailable.Load() {
		return false
	}
	db.bufferedRepositoryRegistryLocked().Store(repository, struct{}{})
	return true
}

func (db *Db) unregisterBufferedRepository(repository *BaseCrudRepository) {
	if db == nil || repository == nil {
		return
	}
	db.resourceMu.Lock()
	registry := db.bufferedRepositoryRegistryLocked()
	registry.Delete(repository)
	db.resourceMu.Unlock()
}

// GetDataSource 返回底层的 *sql.DB 数据源。
func (db *Db) GetDataSource() *sql.DB {
	return db.DataSource
}

// =====================================================
// 第一层：底层 Native SQL 执行
// =====================================================

// ExecuteSqlByStatement 最底层：执行 SqlStatement 中的 SQL 并返回原始行数据（map 格式）
// 这是所有其他查询方法的基础
func (db *Db) ExecuteSqlByStatement(statement *SqlStatement) []map[string]any {
	if !statement.IsQuery {
		return nil
	}

	var results []map[string]any

	// 执行查询语句（不使用 ORM 映射）
	for _, sqlStr := range statement.SqlList {
		rows, err := db.DataSource.Query(sqlStr)
		if err != nil {
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sqlStr))
				db.triggerFaultTolerantReconnect()
			} else {
				LogError("查询执行失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sqlStr))
			}
			continue
		}

		func() {
			defer closeRowsForCompatibility(rows)
			columns, err := rows.Columns()
			if err != nil {
				LogError("获取列名失败: %s", safeErrorForLog(err))
				return
			}

			for rows.Next() {
				var rowMap map[string]any
				if GetCrudPerformanceSettings().Snapshot().EnableRowMapPool || EnableAllocPoolEnabled() {
					m, err := scanRowsToMaps(columns, func(dest []any) error {
						return rows.Scan(dest...)
					})
					if err != nil {
						LogError("扫描行失败: %s", safeErrorForLog(err))
						continue
					}
					rowMap = m
				} else {
					scanTargets := make([]any, len(columns))
					for i := range scanTargets {
						scanTargets[i] = new(any)
					}
					if err := rows.Scan(scanTargets...); err != nil {
						LogError("扫描行失败: %s", safeErrorForLog(err))
						continue
					}
					rowMap = make(map[string]any, len(columns))
					for i, col := range columns {
						rowMap[col] = *scanTargets[i].(*any)
					}
				}
				results = append(results, rowMap)
			}
		}()
	}

	return results
}

// ExecuteUpdate 底层：执行更新/插入/删除操作，返回影响行数。
func (db *Db) ExecuteUpdate(query string, params ...any) (affected int64, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			panicCause, ok := recovered.(error)
			if !ok {
				panicCause = fmt.Errorf("数据库更新 panic: %s", safeValueForLog(recovered))
			}
			affected = 0
			err = NewQueryExceptionWithCause(panicCause, "执行更新发生 panic: "+sqlForError(query))
			LogError("更新执行发生 panic: %s, %s", safeValueForLog(recovered), sqlForRuntimeLog(query))
		}
	}()
	if db == nil || db.DataSource == nil {
		return 0, NewQueryException("数据库连接未初始化")
	}
	databaseGeneration, releaseGeneration, generationErr := db.lockCurrentDatabaseGeneration()
	if generationErr != nil {
		return 0, generationErr
	}
	defer releaseGeneration()

	var result sql.Result
	if settings := GetCrudPerformanceSettings().Snapshot(); settings.EnablePreparedStmtCache {
		result, err = db.execContext(context.Background(), query, params...)
	} else {
		result, err = db.DataSource.Exec(query, params...)
	}
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(query))
			var recoveryErr error
			if manager := db.faultTolerantManagerSnapshot(); manager != nil {
				recoveryErr = manager.recordFailedOperationUnderGenerationLease(&FailedOperation{
					Operation: "ExecuteUpdate",
					SQL:       query,
					Params:    params,
					TableName: "",
				}, databaseGeneration)
				if manager.dbConfig != nil {
					manager.CheckAndReconnect()
				}
			}
			return 0, NewQueryExceptionWithCause(errors.Join(err, recoveryErr), "数据库连接已关闭或不可用，请检查网络连接")
		}
		LogError("更新执行失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(query))
		return 0, NewQueryExceptionWithCause(err, "执行更新失败: "+sqlForError(query))
	}

	affected, err = result.RowsAffected()
	if err != nil {
		LogError("获取影响行数失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(query))
		return 0, NewQueryExceptionWithCause(err, "获取更新影响行数失败: "+sqlForError(query))
	}

	return affected, nil
}

// ExecuteUpdateMultiRowsStrict 以单个 generation 租约严格执行多组参数。
// 任一行失败立即停止并返回已成功行的影响数与原始错误。
func (db *Db) ExecuteUpdateMultiRowsStrict(query string, multiRowParams [][]any) (int64, error) {
	return db.ExecuteUpdateMultiRowsStrictContext(context.Background(), query, multiRowParams)
}

// ExecuteUpdateMultiRowsStrictContext 是大批量逐行 SQL 的生产入口：一次租约、
// fail-fast、严格 RowsAffected、context 取消，并在连接故障时保留 FTM 记录。
func (db *Db) ExecuteUpdateMultiRowsStrictContext(
	ctx context.Context,
	query string,
	multiRowParams [][]any,
) (totalAffected int64, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			panicCause, ok := recovered.(error)
			if !ok {
				panicCause = fmt.Errorf("严格批量更新 panic: %s", safeValueForLog(recovered))
			}
			err = NewQueryExceptionWithCause(panicCause, "严格批量更新发生 panic: "+sqlForError(query))
		}
	}()
	if ctx == nil {
		return 0, NewValidationException("context 不能为 nil")
	}
	if ctxErr := contextCauseError(ctx); ctxErr != nil {
		return 0, NewQueryExceptionWithCause(ctxErr, "严格批量更新上下文已结束")
	}
	if db == nil || db.DataSource == nil {
		return 0, NewQueryException("数据库连接未初始化")
	}
	if len(multiRowParams) == 0 {
		return 0, nil
	}
	databaseGeneration, releaseGeneration, generationErr := db.lockCurrentDatabaseGeneration()
	if generationErr != nil {
		return 0, generationErr
	}
	defer releaseGeneration()

	for rowIndex, params := range multiRowParams {
		if ctxErr := contextCauseError(ctx); ctxErr != nil {
			return totalAffected, NewQueryExceptionWithCause(
				ctxErr,
				fmt.Sprintf("严格批量更新上下文已结束: row=%d", rowIndex),
			)
		}
		result, execErr := db.execContext(ctx, query, params...)
		if execErr != nil {
			var recoveryErr error
			if isConnectionError(execErr) {
				manager := db.faultTolerantManagerSnapshot()
				if manager != nil {
					recoveryErr = manager.recordFailedOperationUnderGenerationLease(&FailedOperation{
						Operation: "ExecuteUpdate",
						SQL:       query,
						Params:    toAnySlice(params),
						TableName: "",
					}, databaseGeneration)
					if manager.dbConfig != nil {
						manager.CheckAndReconnect()
					}
				}
			}
			return totalAffected, NewQueryExceptionWithCause(
				errors.Join(execErr, recoveryErr),
				fmt.Sprintf("严格批量更新失败: row=%d, %s", rowIndex, sqlForError(query)),
			)
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return totalAffected, NewQueryExceptionWithCause(
				affectedErr,
				fmt.Sprintf("严格批量更新读取影响行数失败: row=%d, %s", rowIndex, sqlForError(query)),
			)
		}
		if affected < 0 || affected > int64(^uint64(0)>>1)-totalAffected {
			return totalAffected, NewQueryException(fmt.Sprintf("严格批量更新影响行数溢出: row=%d", rowIndex))
		}
		totalAffected += affected
	}
	return totalAffected, nil
}

func (db *Db) faultTolerantManagerSnapshot() *FaultTolerantManager {
	if db == nil {
		return nil
	}
	db.resourceMu.Lock()
	manager := db.FaultTolerantMgr
	db.resourceMu.Unlock()
	return manager
}

func (db *Db) triggerFaultTolerantReconnect() {
	manager := db.faultTolerantManagerSnapshot()
	if manager != nil && manager.dbConfig != nil {
		manager.CheckAndReconnect()
	}
}

// =====================================================
// 第二层：ORM 快捷方法
// =====================================================

// Query ORM 快捷查询：执行 SQL 并返回原始行数据
func (db *Db) Query(sql string, params ...any) []map[string]any {
	results, err := db.QueryStrict(sql, params...)
	if err != nil {
		LogError("兼容 Query 失败: %s, %s", safeErrorForLog(err), sqlForRuntimeLog(sql))
		if isConnectionError(err) {
			db.triggerFaultTolerantReconnect()
		}
		return []map[string]any{}
	}
	return results
}

// =====================================================
// 第三层：便利方法 - 标量查询（直接返回基本类型）
// =====================================================

// QueryToInt 查询返回单个 int 值
func (db *Db) QueryToInt(sql string, params ...any) int {
	return db.executeQueryToScalar(sql, params, int(0)).(int)
}

// QueryToInt64 查询返回单个 int64 值
func (db *Db) QueryToInt64(sql string, params ...any) int64 {
	return db.executeQueryToScalar(sql, params, int64(0)).(int64)
}

// QueryToFloat64 查询返回单个 float64 值
func (db *Db) QueryToFloat64(sql string, params ...any) float64 {
	return db.executeQueryToScalar(sql, params, float64(0)).(float64)
}

// QueryToString 查询返回单个 string 值
func (db *Db) QueryToString(sql string, params ...any) string {
	return db.executeQueryToScalar(sql, params, "").(string)
}

// QueryToBool 查询返回单个 bool 值
func (db *Db) QueryToBool(sql string, params ...any) bool {
	return db.executeQueryToScalar(sql, params, false).(bool)
}

// QueryToIntSlice 查询返回多个 int 值
func (db *Db) QueryToIntSlice(sql string, params ...any) []int {
	rows, err := db.DataSource.Query(sql, params...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
			db.triggerFaultTolerantReconnect()
		} else {
			LogError("查询执行失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
		}
		return []int{}
	}
	defer closeRowsForCompatibility(rows)

	var results []int
	columns, err := rows.Columns()
	if err != nil {
		LogError("获取列名失败: %s", safeErrorForLog(err))
		return results
	}

	for rows.Next() {
		scanTargets := make([]any, len(columns))
		for i := range scanTargets {
			scanTargets[i] = new(any)
		}

		if err := rows.Scan(scanTargets...); err != nil {
			LogError("扫描行失败: %s", safeErrorForLog(err))
			continue
		}

		rawValue := *scanTargets[0].(*any)
		convertedValue, err := db.convertToPrimitiveType(rawValue, reflect.TypeOf(int(0)))
		if err != nil {
			LogError("转换失败: %s", safeErrorForLog(err))
			continue
		}
		results = append(results, convertedValue.(int))
	}

	return results
}

// QueryToInt64Slice 查询返回多个 int64 值
func (db *Db) QueryToInt64Slice(sql string, params ...any) []int64 {
	rows, err := db.DataSource.Query(sql, params...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
			db.triggerFaultTolerantReconnect()
		} else {
			LogError("查询执行失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
		}
		return []int64{}
	}
	defer closeRowsForCompatibility(rows)

	var results []int64
	columns, err := rows.Columns()
	if err != nil {
		LogError("获取列名失败: %s", safeErrorForLog(err))
		return results
	}

	for rows.Next() {
		scanTargets := make([]any, len(columns))
		for i := range scanTargets {
			scanTargets[i] = new(any)
		}

		if err := rows.Scan(scanTargets...); err != nil {
			LogError("扫描行失败: %s", safeErrorForLog(err))
			continue
		}

		rawValue := *scanTargets[0].(*any)
		convertedValue, err := db.convertToPrimitiveType(rawValue, reflect.TypeOf(int64(0)))
		if err != nil {
			LogError("转换失败: %s", safeErrorForLog(err))
			continue
		}
		results = append(results, convertedValue.(int64))
	}

	return results
}

// QueryToFloat64Slice 查询返回多个 float64 值
func (db *Db) QueryToFloat64Slice(sql string, params ...any) []float64 {
	rows, err := db.DataSource.Query(sql, params...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
			db.triggerFaultTolerantReconnect()
		} else {
			LogError("查询执行失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
		}
		return []float64{}
	}
	defer closeRowsForCompatibility(rows)

	var results []float64
	columns, err := rows.Columns()
	if err != nil {
		LogError("获取列名失败: %s", safeErrorForLog(err))
		return results
	}

	for rows.Next() {
		scanTargets := make([]any, len(columns))
		for i := range scanTargets {
			scanTargets[i] = new(any)
		}

		if err := rows.Scan(scanTargets...); err != nil {
			LogError("扫描行失败: %s", safeErrorForLog(err))
			continue
		}

		rawValue := *scanTargets[0].(*any)
		convertedValue, err := db.convertToPrimitiveType(rawValue, reflect.TypeOf(float64(0)))
		if err != nil {
			LogError("转换失败: %s", safeErrorForLog(err))
			continue
		}
		results = append(results, convertedValue.(float64))
	}

	return results
}

// QueryToStringSlice 查询返回多个 string 值
func (db *Db) QueryToStringSlice(sql string, params ...any) []string {
	rows, err := db.DataSource.Query(sql, params...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
			db.triggerFaultTolerantReconnect()
		} else {
			LogError("查询执行失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
		}
		return []string{}
	}
	defer closeRowsForCompatibility(rows)

	var results []string
	columns, err := rows.Columns()
	if err != nil {
		LogError("获取列名失败: %s", safeErrorForLog(err))
		return results
	}

	for rows.Next() {
		scanTargets := make([]any, len(columns))
		for i := range scanTargets {
			scanTargets[i] = new(any)
		}

		if err := rows.Scan(scanTargets...); err != nil {
			LogError("扫描行失败: %s", safeErrorForLog(err))
			continue
		}

		rawValue := *scanTargets[0].(*any)
		convertedValue, err := db.convertToPrimitiveType(rawValue, reflect.TypeOf(""))
		if err != nil {
			LogError("转换失败: %s", safeErrorForLog(err))
			continue
		}
		results = append(results, convertedValue.(string))
	}

	return results
}

// ExecuteQueryContext 使用指定的 context 执行查询，支持批量参数集。
// 如果 paramsArray 为空，将执行一次无参数查询。
// 如果 returnType 为 nil，将返回原始值（用于 COUNT、SUM 等聚合查询）。
func (db *Db) ExecuteQueryContext(ctx context.Context, query string, paramsArray [][]any, returnType any) []any {
	defer func() {
		if r := recover(); r != nil {
			LogError("查询执行发生 panic: %s, %s", safeValueForLog(r), sqlForRuntimeLog(query))
		}
	}()
	var results []any

	// 如果没有提供参数数组，或者提供了空的参数数组，仍然需要执行一次 SQL（无参数）
	if len(paramsArray) == 0 {
		paramsArray = [][]any{{}}
	}

	// 检测 returnType 是否为基础类型（用于 OLAP 查询如 COUNT、SUM 等）
	if db.isPrimitiveType(returnType) {
		return db.executeQueryPrimitive(ctx, query, paramsArray, returnType)
	}

	// 如果 returnType 为 nil，执行原始值查询（返回原始值或 map）
	if returnType == nil {
		return db.executeQueryRaw(ctx, query, paramsArray)
	}

	for _, params := range paramsArray {
		rows, err := db.queryContext(ctx, query, params...)
		if err != nil {
			// 友好的错误提示
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(query))
				db.triggerFaultTolerantReconnect()
			} else {
				LogError("查询执行失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(query))
			}
			continue
		}

		// 确保 rows 在本次迭代结束时被关闭，避免延迟到函数退出
		func() {
			defer closeRowsForCompatibility(rows)
			// 使用 ORM 映射（假设 OrmBatch 会消费 rows）
			batchResults := OrmHandlerInstance.OrmBatch(rows, returnType)
			results = append(results, batchResults...)
		}()
	}
	return results
}

// ExecuteQueryStrictContext 使用指定 context 执行严格 ORM 查询。
// 任一参数组的 Query、映射、行遍历或关闭失败都会返回 nil 和可检查的错误链。
func (db *Db) ExecuteQueryStrictContext(
	ctx context.Context,
	query string,
	paramsArray [][]any,
	returnType any,
) ([]any, error) {
	if db == nil {
		return nil, NewValidationException("Db 不能为 nil")
	}
	return executeQueryStrictContextWithRunner(ctx, strictDBRowsQueryer{db: db}, query, paramsArray, returnType)
}

// isPrimitiveType 检测是否为基础类型（int, int64, float64, string, bool 等）
func (db *Db) isPrimitiveType(returnType any) bool {
	if returnType == nil {
		return false
	}

	t := reflect.TypeOf(returnType)
	// 处理指针类型
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	kind := t.Kind()
	// 检查是否为基础类型
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64,
		reflect.String,
		reflect.Bool:
		return true
	default:
		return false
	}
}

// executeQueryPrimitive 执行基础类型查询（用于 COUNT、SUM 等 OLAP 查询）
// 只返回第一个值，并转换为指定的基础类型
func (db *Db) executeQueryPrimitive(ctx context.Context, query string, paramsArray [][]any, returnType any) []any {
	var results []any
	targetType := reflect.TypeOf(returnType)
	if targetType.Kind() == reflect.Ptr {
		targetType = targetType.Elem()
	}

	for _, params := range paramsArray {
		rows, err := db.queryContext(ctx, query, params...)
		if err != nil {
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(query))
				db.triggerFaultTolerantReconnect()
			} else {
				LogError("基础类型查询执行失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(query))
			}
			continue
		}

		func() {
			defer closeRowsForCompatibility(rows)
			// 只处理第一行，忽略别名，直接取第一个值
			if rows.Next() {
				// 获取列数
				columns, err := rows.Columns()
				if err != nil {
					LogError("获取列名失败: %s", safeErrorForLog(err))
					return
				}

				// 创建扫描目标（扫描所有列，但只使用第一列）
				scanTargets := make([]any, len(columns))
				for i := range scanTargets {
					scanTargets[i] = new(any)
				}

				err = rows.Scan(scanTargets...)
				if err != nil {
					LogError("扫描基础类型值失败: %s", safeErrorForLog(err))
					return
				}

				// 只取第一列的值
				rawValue := *scanTargets[0].(*any)

				// 转换为目标类型
				convertedValue, err := db.convertToPrimitiveType(rawValue, targetType)
				if err != nil {
					LogError("转换基础类型失败: %s, 目标类型=%s", safeErrorForLog(err), targetType)
					return
				}
				results = append(results, convertedValue)
			}
		}()
	}
	return results
}

// convertToPrimitiveType 将原始值转换为指定的基础类型
func (db *Db) convertToPrimitiveType(rawValue any, targetType reflect.Type) (any, error) {
	if rawValue == nil {
		// 返回目标类型的零值
		return reflect.Zero(targetType).Interface(), nil
	}

	rawVal := reflect.ValueOf(rawValue)

	// 处理 []uint8 (MySQL 返回的字节数组)
	if rawVal.Kind() == reflect.Slice && rawVal.Type().Elem().Kind() == reflect.Uint8 {
		str := string(rawValue.([]byte))
		return db.convertStringToPrimitive(str, targetType)
	}

	// 如果类型匹配，直接返回
	if rawVal.Type().AssignableTo(targetType) {
		return rawValue, nil
	}

	// 尝试转换
	if rawVal.Type().ConvertibleTo(targetType) {
		return rawVal.Convert(targetType).Interface(), nil
	}

	// 如果是字符串，尝试解析
	if rawVal.Kind() == reflect.String {
		return db.convertStringToPrimitive(rawVal.String(), targetType)
	}

	return nil, fmt.Errorf("无法将 %T 转换为 %s", rawValue, targetType)
}

// convertStringToPrimitive 将字符串转换为指定的基础类型
func (db *Db) convertStringToPrimitive(str string, targetType reflect.Type) (any, error) {
	targetKind := targetType.Kind()

	switch targetKind {
	case reflect.String:
		return str, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		val, err := strconv.ParseInt(str, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("无法将字符串转换为整数: %w", err)
		}
		return reflect.ValueOf(val).Convert(targetType).Interface(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		val, err := strconv.ParseUint(str, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("无法将字符串转换为无符号整数: %w", err)
		}
		return reflect.ValueOf(val).Convert(targetType).Interface(), nil
	case reflect.Float32, reflect.Float64:
		val, err := strconv.ParseFloat(str, 64)
		if err != nil {
			return nil, fmt.Errorf("无法将字符串转换为浮点数: %w", err)
		}
		return reflect.ValueOf(val).Convert(targetType).Interface(), nil
	case reflect.Bool:
		val, err := strconv.ParseBool(str)
		if err != nil {
			return nil, fmt.Errorf("无法将字符串转换为布尔值: %w", err)
		}
		return val, nil
	default:
		return nil, fmt.Errorf("不支持的目标类型: %s", targetType)
	}
}

// executeQueryRaw 执行原始值查询（用于 COUNT、SUM 等聚合查询）
func (db *Db) executeQueryRaw(ctx context.Context, query string, paramsArray [][]any) []any {
	var results []any
	for _, params := range paramsArray {
		rows, err := db.queryContext(ctx, query, params...)
		if err != nil {
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(query))
				db.triggerFaultTolerantReconnect()
			} else {
				LogError("原始查询执行失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(query))
			}
			continue
		}

		func() {
			defer closeRowsForCompatibility(rows)
			columns, err := rows.Columns()
			if err != nil {
				LogError("获取列名失败: %s", safeErrorForLog(err))
				return
			}

			for rows.Next() {
				scratch := acquireScanScratch(len(columns))
				dest := scratch.dest
				for i := range dest {
					dest[i] = scratch.discardPtr(i)
				}
				if err := rows.Scan(dest...); err != nil {
					releaseScanScratch(scratch)
					LogError("扫描行失败: %s", safeErrorForLog(err))
					continue
				}
				if len(columns) == 1 {
					val := *scratch.discardPtr(0)
					releaseScanScratch(scratch)
					results = append(results, val)
				} else if GetCrudPerformanceSettings().Snapshot().EnableRowMapPool || EnableAllocPoolEnabled() {
					pooled := acquireRowMap(len(columns))
					for i, col := range columns {
						pooled[col] = *scratch.discardPtr(i)
					}
					releaseScanScratch(scratch)
					results = append(results, copyRowMap(pooled))
					releaseRowMap(pooled)
				} else {
					rowMap := make(map[string]any, len(columns))
					for i, col := range columns {
						rowMap[col] = *scratch.discardPtr(i)
					}
					releaseScanScratch(scratch)
					results = append(results, rowMap)
				}
			}
		}()
	}
	return results
}

// ExecuteQueryVariadic 使用单组可变参数执行查询并返回映射结果。
func (db *Db) ExecuteQueryVariadic(query string, returnType any, params ...any) []any {
	// 将可变参数包装成单条 paramsArray
	return db.ExecuteQueryContext(context.Background(), query, [][]any{params}, returnType)
}

// ExecuteQueryTyped 执行查询并返回泛型类型切片，适用于 Go 泛型调用。
// 使用示例：ExecuteQueryTyped[MyEntity](db, ctx, "SELECT ...", params...)
func ExecuteQueryTyped[T any](db *Db, ctx context.Context, query string, params ...any) ([]T, error) {
	var tPtr *T
	results := db.ExecuteQueryContext(ctx, query, [][]any{params}, tPtr)
	out := make([]T, 0, len(results))
	for i, r := range results {
		switch v := r.(type) {
		case T:
			out = append(out, v)
		case *T:
			if v == nil {
				continue
			}
			out = append(out, *v)
		default:
			return nil, fmt.Errorf("结果无法转换为目标类型 (index=%d): %T", i, r)
		}
	}
	return out, nil
}

// ExecuteQueryTypedStrict 执行严格泛型查询，SQL 与映射错误均通过 error 返回。
func ExecuteQueryTypedStrict[T any](db *Db, ctx context.Context, query string, params ...any) ([]T, error) {
	if db == nil {
		return nil, NewValidationException("Db 不能为 nil")
	}
	targetType := reflect.TypeOf((*T)(nil)).Elem()
	entityType := targetType
	if entityType.Kind() == reflect.Ptr {
		entityType = entityType.Elem()
	}
	if entityType.Kind() != reflect.Struct {
		return nil, NewValidationException(fmt.Sprintf("严格泛型查询目标必须是 struct 或 *struct，实际类型: %s", targetType))
	}
	prototype := reflect.New(entityType).Interface()
	results, err := db.ExecuteQueryStrictContext(ctx, query, [][]any{params}, prototype)
	if err != nil {
		return nil, err
	}

	out := make([]T, 0, len(results))
	for index, result := range results {
		switch value := result.(type) {
		case T:
			out = append(out, value)
		case *T:
			if value == nil {
				return nil, NewQueryException(fmt.Sprintf("严格查询结果为 nil: index=%d", index))
			}
			out = append(out, *value)
		default:
			return nil, NewQueryException(fmt.Sprintf("严格查询结果无法转换为目标类型: index=%d, type=%T", index, result))
		}
	}
	return out, nil
}

var _ StrictQueryer = (*Db)(nil)

// ExecuteQueryByStatement 使用 SqlStatement 执行查询并返回映射结果。
// 返回 []map[string]any 格式的原始查询结果，不进行 ORM 映射。
func (db *Db) ExecuteQueryByStatement(statement *SqlStatement) []map[string]any {
	if !statement.IsQuery {
		return nil
	}

	var results []map[string]any

	// 执行查询语句（不使用 ORM 映射）
	for _, sqlStr := range statement.SqlList {
		rows, err := db.DataSource.Query(sqlStr)
		if err != nil {
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sqlStr))
				db.triggerFaultTolerantReconnect()
			} else {
				LogError("查询执行失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sqlStr))
			}
			continue
		}

		func() {
			defer closeRowsForCompatibility(rows)
			columns, err := rows.Columns()
			if err != nil {
				LogError("获取列名失败: %s", safeErrorForLog(err))
				return
			}

			for rows.Next() {
				var rowMap map[string]any
				if GetCrudPerformanceSettings().Snapshot().EnableRowMapPool || EnableAllocPoolEnabled() {
					m, err := scanRowsToMaps(columns, func(dest []any) error {
						return rows.Scan(dest...)
					})
					if err != nil {
						LogError("扫描行失败: %s", safeErrorForLog(err))
						continue
					}
					rowMap = m
				} else {
					scanTargets := make([]any, len(columns))
					for i := range scanTargets {
						scanTargets[i] = new(any)
					}
					if err := rows.Scan(scanTargets...); err != nil {
						LogError("扫描行失败: %s", safeErrorForLog(err))
						continue
					}
					rowMap = make(map[string]any, len(columns))
					for i, col := range columns {
						rowMap[col] = *scanTargets[i].(*any)
					}
				}
				results = append(results, rowMap)
			}
		}()
	}

	return results
}

// ExecuteUpdateByStatement 使用 SqlStatement 执行更新语句，返回受影响行数。
func (db *Db) ExecuteUpdateByStatement(statement *SqlStatement) (totalAffected int) {
	defer func() {
		if recovered := recover(); recovered != nil {
			LogError("ExecuteUpdateByStatement 发生 panic，停止后续写入: %s", safeValueForLog(recovered))
		}
	}()
	if db == nil || db.DataSource == nil {
		LogError("ExecuteUpdateByStatement 失败: 数据库连接未初始化")
		return 0
	}
	if statement == nil {
		LogError("ExecuteUpdateByStatement 失败: SqlStatement 不能为 nil")
		return 0
	}
	if statement.IsQuery {
		return 0
	}
	databaseGeneration, releaseGeneration, generationErr := db.lockCurrentDatabaseGeneration()
	if generationErr != nil {
		LogError("ExecuteUpdateByStatement generation 校验失败: %v", generationErr)
		return 0
	}
	defer releaseGeneration()
	for _, q := range statement.SqlList {
		result, err := db.DataSource.Exec(q)
		if err != nil {
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(q))
				if manager := db.faultTolerantManagerSnapshot(); manager != nil {
					recordErr := manager.recordFailedOperationUnderGenerationLease(&FailedOperation{
						Operation: "ExecuteUpdate",
						SQL:       q,
						Params:    []any{},
						TableName: "",
					}, databaseGeneration)
					if recordErr != nil {
						LogError("失败操作未能持久化，停止后续语句: %v", recordErr)
					}
					if manager.dbConfig != nil {
						manager.CheckAndReconnect()
					}
				}
			} else {
				LogError("ExecuteUpdateByStatement 执行失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(q))
			}
			return totalAffected
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			LogError("ExecuteUpdateByStatement 获取影响行数失败，停止后续语句: %s (%s)", safeErrorForLog(affectedErr), sqlForRuntimeLog(q))
			return totalAffected
		}
		totalAffected += int(affected)
	}
	return totalAffected
}

// ExecuteUpdateMultiRows 使用 SQL 与多行参数执行批量更新，返回总影响行数。
func (db *Db) ExecuteUpdateMultiRows(query string, multiRowParams [][]any) (totalAffected int) {
	defer func() {
		if recovered := recover(); recovered != nil {
			LogError("批量更新发生 panic，停止后续写入: %s, %s", safeValueForLog(recovered), sqlForRuntimeLog(query))
		}
	}()
	if db == nil || db.DataSource == nil {
		LogError("ExecuteUpdateMultiRows 失败: 数据库连接未初始化")
		return 0
	}
	databaseGeneration, releaseGeneration, generationErr := db.lockCurrentDatabaseGeneration()
	if generationErr != nil {
		LogError("ExecuteUpdateMultiRows generation 校验失败: %v", generationErr)
		return 0
	}
	defer releaseGeneration()
	for _, params := range multiRowParams {
		result, err := db.DataSource.Exec(query, params...)
		if err != nil {
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(query))
				if manager := db.faultTolerantManagerSnapshot(); manager != nil {
					recordErr := manager.recordFailedOperationUnderGenerationLease(&FailedOperation{
						Operation: "ExecuteUpdate",
						SQL:       query,
						Params:    toAnySlice(params),
						TableName: "",
					}, databaseGeneration)
					if recordErr != nil {
						LogError("失败操作未能持久化，停止后续批量写入: %v", recordErr)
					}
					if manager.dbConfig != nil {
						manager.CheckAndReconnect()
					}
				}
			} else {
				LogError("批量更新失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(query))
			}
			return totalAffected
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			LogError("批量更新获取影响行数失败，停止后续写入: %s (%s)", safeErrorForLog(affectedErr), sqlForRuntimeLog(query))
			return totalAffected
		}
		totalAffected += int(affected)
	}
	return totalAffected
}

// ExecuteUpdateMultiRowsNamed 使用 SQL 与多行命名参数执行批量更新，返回总影响行数。
// 使用命名参数方式，SQL 中用 {paramName} 表示占位符，参数通过 []map[string]any 传递
// 例如：sql = "UPDATE users SET name={name}, age={age} WHERE id={userId}"
//
//	params = []map[string]any{
//	    {"name": "Alice", "age": 25, "userId": 1},
//	    {"name": "Bob", "age": 30, "userId": 2},
//	}
func (db *Db) ExecuteUpdateMultiRowsNamed(sql string, paramsList []map[string]any) (totalAffected int) {
	defer func() {
		if recovered := recover(); recovered != nil {
			LogError("命名参数批量更新发生 panic，停止后续写入: %s, %s", safeValueForLog(recovered), sqlForRuntimeLog(sql))
		}
	}()
	if db == nil || db.DataSource == nil {
		LogError("ExecuteUpdateMultiRowsNamed 失败: 数据库连接未初始化")
		return 0
	}
	databaseGeneration, releaseGeneration, generationErr := db.lockCurrentDatabaseGeneration()
	if generationErr != nil {
		LogError("ExecuteUpdateMultiRowsNamed generation 校验失败: %v", generationErr)
		return 0
	}
	defer releaseGeneration()
	for _, params := range paramsList {
		newSQL, values, err := replaceSqlNamedParameters(sql, params)
		if err != nil {
			LogError("命名参数替换失败，停止后续写入: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
			return totalAffected
		}

		result, err := db.DataSource.Exec(newSQL, values...)
		if err != nil {
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
				if manager := db.faultTolerantManagerSnapshot(); manager != nil {
					recordErr := manager.recordFailedOperationUnderGenerationLease(&FailedOperation{
						Operation: "ExecuteUpdate",
						SQL:       newSQL,
						Params:    values,
						TableName: "",
					}, databaseGeneration)
					if recordErr != nil {
						LogError("失败操作未能持久化，停止后续命名参数写入: %v", recordErr)
					}
					if manager.dbConfig != nil {
						manager.CheckAndReconnect()
					}
				}
			} else {
				LogError("命名参数批量更新失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
			}
			return totalAffected
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			LogError("命名参数批量更新获取影响行数失败，停止后续写入: %s (%s)", safeErrorForLog(affectedErr), sqlForRuntimeLog(sql))
			return totalAffected
		}
		totalAffected += int(affected)
	}
	return totalAffected
}

// ExecuteOriginalUpdate 向后兼容：ExecuteUpdateMultiRows 的别名
func (db *Db) ExecuteOriginalUpdate(query string, multiRowParams [][]any) int {
	return db.ExecuteUpdateMultiRows(query, multiRowParams)
}

// ExecuteWithConnection 提供对低级 *sql.Conn 的回调入口。
func (db *Db) ExecuteWithConnection(fn func(*sql.Conn) error) (err error) {
	if db == nil || db.DataSource == nil {
		return NewQueryException("数据库连接未初始化")
	}
	if fn == nil {
		return NewValidationException("连接回调不能为 nil")
	}
	_, releaseGeneration, generationErr := db.lockCurrentDatabaseGeneration()
	if generationErr != nil {
		return generationErr
	}
	defer releaseGeneration()

	conn, err := db.DataSource.Conn(context.Background())
	if err != nil {
		return NewQueryExceptionWithCause(err, "获取数据库连接失败")
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			err = errors.Join(err, NewQueryExceptionWithCause(closeErr, "关闭数据库连接失败"))
		}
	}()
	callbackErr := fn(conn)
	if callbackErr != nil {
		return NewQueryExceptionWithCause(callbackErr, "数据库连接回调执行失败")
	}
	return nil
}

// ExecuteQuery 使用批量参数集合执行查询（每组参数单独执行一次），并将结果映射为 returnType 指定的类型。
func (db *Db) ExecuteQuery(query string, paramsArray [][]any, returnType any) []any {
	// 保持向后兼容：默认使用 background context
	return db.ExecuteQueryContext(context.Background(), query, paramsArray, returnType)
}

// ExecuteQuerySingle 执行单行查询并返回结果，找不到时返回类型默认值。
func (db *Db) ExecuteQuerySingle(query string, params []any, returnType any) any {
	results := db.ExecuteQuery(query, [][]any{params}, returnType)
	if len(results) > 0 {
		return results[0]
	}
	return getDefaultValue(returnType)
}

// ExecuteQuerySingleOrNull 执行单行查询并返回结果或 nil。
func (db *Db) ExecuteQuerySingleOrNull(query string, params []any, returnType any) any {
	results := db.ExecuteQuery(query, [][]any{params}, returnType)
	if len(results) > 0 {
		return results[0]
	}
	return nil
}

// ExecuteQuery 简化版查询方法（单组参数）
func (db *Db) ExecuteQuerySimple(query string, params []any, returnType any) []any {
	return db.ExecuteQuery(query, [][]any{params}, returnType)
}

// Close 关闭底层数据库连接，并在需要时停止容错管理器、WAL 与 Session 定时刷写。
func (db *Db) Close() error {
	if db == nil {
		return nil
	}
	db.closeOnce.Do(func() {
		db.rotationMu.Lock()
		defer db.rotationMu.Unlock()
		db.resourceMu.Lock()
		db.closing = true
		sessionRepository := db.SessionRepo
		writeJournal := db.WriteJournal
		faultTolerantManager := db.FaultTolerantMgr
		bufferedRepositories := db.bufferedRepositoryRegistryLocked()
		db.resourceMu.Unlock()
		// 最先发布 unavailable，任何在此之后开始的 managed write 都会被
		// 拒绝；已持有读租约的写入由下面的 generation 写锁严格排空。
		db.generationUnavailable.Store(true)
		UnregisterDbForConnectionPool(db)

		var closeErrors []error
		if sessionRepository != nil {
			sessionRepository.CloseAdmissionAndWait()
			sessionRepository.Stop()
			sessionRepository.generationMu.Lock()
		}
		db.generationMu.Lock()
		databaseGeneration := db.databaseGeneration

		// 持有两个 generation 写锁执行最终 Session/WB 刷盘。内部路径不会
		// 重入读锁，因此 Close 与最终 flush 之间不存在新写入窗口。
		var sessionFlushErr error
		if sessionRepository != nil {
			sessionFlushErr = sessionRepository.flushAllForGenerationTransitionUnderLease(databaseGeneration)
			if sessionFlushErr != nil {
				closeErrors = append(closeErrors, NewDb233ExceptionWithCause(sessionFlushErr, "刷新 Session 数据失败"))
			}
		}

		bufferedRepositories.Range(func(key, _ any) bool {
			repository, ok := key.(*BaseCrudRepository)
			if !ok || repository == nil {
				return true
			}
			if err := repository.closeUnderGenerationLease(databaseGeneration); err != nil {
				closeErrors = append(closeErrors, NewDb233ExceptionWithCause(err, "关闭写缓冲失败"))
			}
			return true
		})
		if writeJournal != nil {
			if remaining, err := writeJournal.drainUnderGenerationLeaseStrict(databaseGeneration); err != nil || remaining != 0 {
				if err == nil {
					err = NewQueryException(fmt.Sprintf("WAL 排空后仍有 %d 条待回放", remaining))
				}
				closeErrors = append(closeErrors, fmt.Errorf("关闭前排空本地 WAL（remaining=%d）: %w", remaining, err))
			}
		}
		if faultTolerantManager != nil {
			if remaining, err := faultTolerantManager.drainUnderGenerationLeaseStrict(databaseGeneration); err != nil || remaining != 0 {
				if err == nil {
					err = NewQueryException(fmt.Sprintf("失败操作排空后仍有 %d 条待重试", remaining))
				}
				closeErrors = append(closeErrors, fmt.Errorf("关闭前排空失败操作（remaining=%d）: %w", remaining, err))
			}
		}
		if sessionRepository != nil && sessionFlushErr == nil {
			sessionRepository.clearAfterSuccessfulShutdown()
		}
		db.generationErr = ErrDatabaseGenerationBlocked
		db.closingState.Store(true)
		db.generationMu.Unlock()
		if sessionRepository != nil {
			sessionRepository.generationMu.Unlock()
		}

		// WAL/FTM 必须活到所有 Session/WriteBuffer 冲刷完成；否则关闭期间的
		// 连接失败无法落恢复队列，或已入 WAL 的数据尚未写库就停止回放。
		if writeJournal != nil {
			if err := writeJournal.StopStrict(); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("停止本地 WAL: %w", err))
			}
		}
		db.resourceMu.Lock()
		if db.FaultTolerantMgr == faultTolerantManager {
			db.FaultTolerantMgr = nil
		}
		db.resourceMu.Unlock()
		if faultTolerantManager != nil {
			if err := faultTolerantManager.StopStrict(); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("停止容错管理器: %w", err))
			}
		}

		if db.DataSource != nil {
			if err := GetPreparedStmtCache().RemoveDBStrict(db.DataSource); err != nil {
				closeErrors = append(closeErrors, NewDb233ExceptionWithCause(err, "关闭预编译语句失败"))
			}
			if err := db.DataSource.Close(); err != nil {
				closeErrors = append(closeErrors, NewDb233ExceptionWithCause(err, "关闭数据库连接失败"))
			}
		}
		db.closeErr = errors.Join(closeErrors...)
	})
	return db.closeErr
}

// EnableFaultTolerance 启用容错管理器。
func (db *Db) EnableFaultTolerance(dbConfig *DbConnectionConfig) {
	if err := db.EnableFaultToleranceStrict(dbConfig); err != nil {
		LogError("容错管理器启用失败: %s", safeErrorForLog(err))
	}
}

// EnableFaultToleranceStrict 启用容错管理器并传播恢复文件/启动错误。
func (db *Db) EnableFaultToleranceStrict(dbConfig *DbConnectionConfig) error {
	if db == nil {
		return NewValidationException("Db 不能为 nil")
	}
	if dbConfig == nil {
		return NewValidationException("DbConnectionConfig 不能为 nil")
	}

	db.resourceMu.Lock()
	if db.closing || db.closingState.Load() {
		db.resourceMu.Unlock()
		return ErrCrudRepositoryClosed
	}
	if db.FaultTolerantMgr != nil {
		db.resourceMu.Unlock()
		return nil
	}
	manager := NewFaultTolerantManager(db, dbConfig)
	// manager 仅在 StartStrict 完全成功后发布；持 resourceMu 使 Close/Disable
	// 无法观察到半初始化对象。
	if err := manager.StartStrict(); err != nil {
		db.resourceMu.Unlock()
		stopErr := manager.StopStrict()
		return errors.Join(err, stopErr)
	}
	db.FaultTolerantMgr = manager
	db.resourceMu.Unlock()
	LogInfo("容错管理器已启用")
	return nil
}

// DisableFaultTolerance 停用容错管理器。
func (db *Db) DisableFaultTolerance() {
	if err := db.DisableFaultToleranceStrict(); err != nil {
		LogError("容错管理器停用失败: %s", safeErrorForLog(err))
	}
}

// DisableFaultToleranceStrict 原子摘除管理器，再在锁外等待后台协程停止。
func (db *Db) DisableFaultToleranceStrict() error {
	if db == nil {
		return NewValidationException("Db 不能为 nil")
	}
	// 与 Close/代次轮换共用相同的锁顺序。generation 写锁保证
	// 所有已准入的写入都已完成容错记录，然后才能摘除 manager；
	// 新写入在解锁后只会观察到 nil，不会与 Stop 竞态。
	db.rotationMu.Lock()
	defer db.rotationMu.Unlock()
	db.generationMu.Lock()
	db.resourceMu.Lock()
	if db.closing || db.closingState.Load() {
		db.resourceMu.Unlock()
		db.generationMu.Unlock()
		return ErrCrudRepositoryClosed
	}
	manager := db.FaultTolerantMgr
	db.FaultTolerantMgr = nil
	db.resourceMu.Unlock()
	db.generationMu.Unlock()
	if manager == nil {
		return nil
	}
	if err := manager.StopStrict(); err != nil {
		return err
	}
	LogInfo("容错管理器已停用")
	return nil
}

// toAnySlice 辅助函数，将 []any 复制为新的 []any 切片。
func toAnySlice(params []any) []any {
	if len(params) == 0 {
		return []any{}
	}
	result := make([]any, 0, len(params))
	result = append(result, params...)
	return result
}

// getDefaultValue 返回常见类型的默认 Go 值（用于单行查询未命中时）。
func getDefaultValue(t any) any {
	switch t.(type) {
	case int:
		return 0
	case int64:
		return int64(0)
	case string:
		return ""
	case bool:
		return false
	default:
		return nil
	}
}

// =====================================================
// 便利查询方法：直接返回标量类型
// =====================================================

// =====================================================
// 向后兼容：旧名称的别名
// =====================================================

// ExecuteQueryToInt 向后兼容：使用 QueryToInt 替代
func (db *Db) ExecuteQueryToInt(sql string, params ...any) int {
	return db.QueryToInt(sql, params...)
}

// ExecuteQueryToInt64 向后兼容：使用 QueryToInt64 替代
func (db *Db) ExecuteQueryToInt64(sql string, params ...any) int64 {
	return db.QueryToInt64(sql, params...)
}

// ExecuteQueryToFloat64 向后兼容：使用 QueryToFloat64 替代
func (db *Db) ExecuteQueryToFloat64(sql string, params ...any) float64 {
	return db.QueryToFloat64(sql, params...)
}

// ExecuteQueryToString 向后兼容：使用 QueryToString 替代
func (db *Db) ExecuteQueryToString(sql string, params ...any) string {
	return db.QueryToString(sql, params...)
}

// ExecuteQueryToBool 向后兼容：使用 QueryToBool 替代
func (db *Db) ExecuteQueryToBool(sql string, params ...any) bool {
	return db.QueryToBool(sql, params...)
}

// ExecuteQueryToIntSlice 向后兼容：使用 QueryToIntSlice 替代
func (db *Db) ExecuteQueryToIntSlice(sql string, params ...any) []int {
	return db.QueryToIntSlice(sql, params...)
}

// ExecuteQueryToInt64Slice 向后兼容：使用 QueryToInt64Slice 替代
func (db *Db) ExecuteQueryToInt64Slice(sql string, params ...any) []int64 {
	return db.QueryToInt64Slice(sql, params...)
}

// ExecuteQueryToFloat64Slice 向后兼容：使用 QueryToFloat64Slice 替代
func (db *Db) ExecuteQueryToFloat64Slice(sql string, params ...any) []float64 {
	return db.QueryToFloat64Slice(sql, params...)
}

// ExecuteQueryToStringSlice 向后兼容：使用 QueryToStringSlice 替代
func (db *Db) ExecuteQueryToStringSlice(sql string, params ...any) []string {
	return db.QueryToStringSlice(sql, params...)
}

// ExecuteQueryToInt64ByStatement 向后兼容：使用 ExecuteSqlByStatement 替代
func (db *Db) ExecuteQueryToInt64ByStatement(statement *SqlStatement) int64 {
	rows := db.ExecuteSqlByStatement(statement)
	if len(rows) > 0 {
		return db.extractScalarValue(rows[0], int64(0)).(int64)
	}
	return 0
}

// ExecuteQueryToStringByStatement 向后兼容：使用 ExecuteSqlByStatement 替代
func (db *Db) ExecuteQueryToStringByStatement(statement *SqlStatement) string {
	rows := db.ExecuteSqlByStatement(statement)
	if len(rows) > 0 {
		return db.extractScalarValue(rows[0], "").(string)
	}
	return ""
}

// =====================================================
// 内部辅助方法：类型转换工具
// =====================================================

// executeQueryToScalar 通用的标量类型查询方法
// 执行查询并从第一行第一列获取值，然后转换为指定的基础类型
// defaultValue: 用来推断目标类型，查询无结果时返回该类型的零值
func (db *Db) executeQueryToScalar(sql string, params []any, defaultValue any) any {
	rows, err := db.DataSource.Query(sql, params...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
			db.triggerFaultTolerantReconnect()
		} else {
			LogError("标量查询执行失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
		}
		return defaultValue
	}
	defer closeRowsForCompatibility(rows)

	// 获取列信息
	columns, err := rows.Columns()
	if err != nil {
		LogError("获取列名失败: %s", safeErrorForLog(err))
		return defaultValue
	}

	// 如果没有行，返回默认值
	if !rows.Next() {
		return defaultValue
	}

	// 创建扫描目标（只需要第一列）
	if EnableAllocPoolEnabled() && len(columns) > 0 {
		scratch := acquireScanScratch(len(columns))
		defer releaseScanScratch(scratch)
		dest := scratch.dest
		for i := range dest {
			dest[i] = scratch.discardPtr(i)
		}
		if err := rows.Scan(dest...); err != nil {
			LogError("扫描标量值失败: %s", safeErrorForLog(err))
			return defaultValue
		}
		rawValue := *scratch.discardPtr(0)
		convertedValue, err := db.convertToPrimitiveType(rawValue, reflect.TypeOf(defaultValue))
		if err != nil {
			LogError("标量值转换失败: %s", safeErrorForLog(err))
			return defaultValue
		}
		return convertedValue
	}

	scanTargets := make([]any, len(columns))
	for i := range scanTargets {
		scanTargets[i] = new(any)
	}

	if err := rows.Scan(scanTargets...); err != nil {
		LogError("扫描标量值失败: %s", safeErrorForLog(err))
		return defaultValue
	}

	// 获取第一列的原始值
	rawValue := *scanTargets[0].(*any)

	// 推断目标类型并转换
	targetType := reflect.TypeOf(defaultValue)
	convertedValue, err := db.convertToPrimitiveType(rawValue, targetType)
	if err != nil {
		LogError("转换标量类型失败: %s, 目标类型=%s", safeErrorForLog(err), targetType)
		return defaultValue
	}

	return convertedValue
}

// replaceSqlNamedParameters 将 SQL 中的命名占位符 {paramName} 替换为 ? 并返回参数值数组
// 例如：SQL="SELECT * FROM users WHERE id={userId} AND name={userName}"
// params=map{"userId": 123, "userName": "Alice"}
// 返回：newSQL="SELECT * FROM users WHERE id=? AND name=?", values=[123, "Alice"]
func replaceSqlNamedParameters(sql string, params map[string]any) (string, []any, error) {
	var newSQL string
	var values []any
	i := 0

	for i < len(sql) {
		// 查找下一个占位符
		startIdx := -1
		for j := i; j < len(sql); j++ {
			if sql[j] == '{' {
				startIdx = j
				break
			}
		}

		if startIdx == -1 {
			// 没有更多占位符，直接添加剩余部分
			newSQL += sql[i:]
			break
		}

		// 找到结束的 }
		endIdx := -1
		for j := startIdx + 1; j < len(sql); j++ {
			if sql[j] == '}' {
				endIdx = j
				break
			}
		}

		if endIdx == -1 {
			return "", nil, fmt.Errorf("SQL 在字节位置 %d 存在未闭合的占位符: %s", startIdx, sqlForError(sql))
		}

		// 提取参数名
		paramName := sql[startIdx+1 : endIdx]

		// 检查参数是否存在
		value, exists := params[paramName]
		if !exists {
			return "", nil, fmt.Errorf("缺少必需的参数：%s", paramName)
		}

		// 添加 SQL 片段和替换占位符
		newSQL += sql[i:startIdx] + "?"
		values = append(values, value)

		i = endIdx + 1
	}

	return newSQL, values, nil
}

// =====================================================
// 命名参数查询方法
// =====================================================

// QueryNamed 执行带命名参数的 SQL 查询
// 例如：db.QueryNamed("SELECT * FROM users WHERE id={userId} AND status={status}", map[string]any{"userId": 123, "status": "active"})
func (db *Db) QueryNamed(sql string, params map[string]any) []map[string]any {
	results, err := db.QueryNamedStrict(sql, params)
	if err != nil {
		LogError("兼容 QueryNamed 失败: %s, %s", safeErrorForLog(err), sqlForRuntimeLog(sql))
		if isConnectionError(err) {
			db.triggerFaultTolerantReconnect()
		}
		return []map[string]any{}
	}
	return results
}

// QueryNamedToInt64 执行带命名参数的 SQL 查询，返回单个 int64 值
func (db *Db) QueryNamedToInt64(sql string, params map[string]any) int64 {
	rows := db.QueryNamed(sql, params)
	if len(rows) > 0 {
		return db.extractScalarValue(rows[0], int64(0)).(int64)
	}
	return 0
}

// QueryNamedToString 执行带命名参数的 SQL 查询，返回单个 string 值
func (db *Db) QueryNamedToString(sql string, params map[string]any) string {
	rows := db.QueryNamed(sql, params)
	if len(rows) > 0 {
		return db.extractScalarValue(rows[0], "").(string)
	}
	return ""
}

// QueryNamedToInt 执行带命名参数的 SQL 查询，返回单个 int 值
func (db *Db) QueryNamedToInt(sql string, params map[string]any) int {
	rows := db.QueryNamed(sql, params)
	if len(rows) > 0 {
		return db.extractScalarValue(rows[0], int(0)).(int)
	}
	return 0
}

// QueryNamedToFloat64 执行带命名参数的 SQL 查询，返回单个 float64 值
func (db *Db) QueryNamedToFloat64(sql string, params map[string]any) float64 {
	rows := db.QueryNamed(sql, params)
	if len(rows) > 0 {
		return db.extractScalarValue(rows[0], float64(0)).(float64)
	}
	return 0
}

// QueryNamedToInt64Slice 执行带命名参数的 SQL 查询，返回 []int64
func (db *Db) QueryNamedToInt64Slice(sql string, params map[string]any) []int64 {
	newSQL, values, err := replaceSqlNamedParameters(sql, params)
	if err != nil {
		LogError("参数替换失败: %s", safeErrorForLog(err))
		return []int64{}
	}

	rows, err := db.DataSource.Query(newSQL, values...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
			db.triggerFaultTolerantReconnect()
		} else {
			LogError("查询执行失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
		}
		return []int64{}
	}
	defer closeRowsForCompatibility(rows)

	var results []int64
	for rows.Next() {
		var val int64
		if err := rows.Scan(&val); err != nil {
			LogError("扫描行失败: %s", safeErrorForLog(err))
			continue
		}
		results = append(results, val)
	}

	return results
}

// QueryNamedToStringSlice 执行带命名参数的 SQL 查询，返回 []string
func (db *Db) QueryNamedToStringSlice(sql string, params map[string]any) []string {
	newSQL, values, err := replaceSqlNamedParameters(sql, params)
	if err != nil {
		LogError("参数替换失败: %s", safeErrorForLog(err))
		return []string{}
	}

	rows, err := db.DataSource.Query(newSQL, values...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
			db.triggerFaultTolerantReconnect()
		} else {
			LogError("查询执行失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
		}
		return []string{}
	}
	defer closeRowsForCompatibility(rows)

	var results []string
	for rows.Next() {
		var val string
		if err := rows.Scan(&val); err != nil {
			LogError("扫描行失败: %s", safeErrorForLog(err))
			continue
		}
		results = append(results, val)
	}

	return results
}

// ExecuteUpdateNamed 执行带命名参数的 SQL 更新语句
// 例如：db.ExecuteUpdateNamed("UPDATE users SET name={name} WHERE id={userId}", map[string]any{"name": "Bob", "userId": 123})
func (db *Db) ExecuteUpdateNamed(sql string, params map[string]any) (affected int64, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			panicCause, ok := recovered.(error)
			if !ok {
				panicCause = fmt.Errorf("命名参数数据库更新 panic: %s", safeValueForLog(recovered))
			}
			affected = 0
			err = NewQueryExceptionWithCause(panicCause, "执行命名参数更新发生 panic: "+sqlForError(sql))
			LogError("命名参数更新发生 panic: %s, %s", safeValueForLog(recovered), sqlForRuntimeLog(sql))
		}
	}()
	if db == nil || db.DataSource == nil {
		return 0, NewQueryException("数据库连接未初始化")
	}
	databaseGeneration, releaseGeneration, generationErr := db.lockCurrentDatabaseGeneration()
	if generationErr != nil {
		return 0, generationErr
	}
	defer releaseGeneration()

	newSQL, values, err := replaceSqlNamedParameters(sql, params)
	if err != nil {
		LogError("命名参数替换失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
		return 0, NewQueryExceptionWithCause(err, "命名参数替换失败: "+sqlForError(sql))
	}

	result, err := db.DataSource.Exec(newSQL, values...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
			var recoveryErr error
			if manager := db.faultTolerantManagerSnapshot(); manager != nil {
				recoveryErr = manager.recordFailedOperationUnderGenerationLease(&FailedOperation{
					Operation: "ExecuteUpdate",
					SQL:       newSQL,
					Params:    values,
					TableName: "",
				}, databaseGeneration)
				if manager.dbConfig != nil {
					manager.CheckAndReconnect()
				}
			}
			return 0, NewQueryExceptionWithCause(errors.Join(err, recoveryErr), "数据库连接已关闭或不可用，请检查网络连接")
		}
		LogError("更新执行失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
		return 0, NewQueryExceptionWithCause(err, "执行更新失败: "+sqlForError(sql))
	}

	affected, err = result.RowsAffected()
	if err != nil {
		LogError("获取影响行数失败: %s (%s)", safeErrorForLog(err), sqlForRuntimeLog(sql))
		return 0, NewQueryExceptionWithCause(err, "获取更新影响行数失败: "+sqlForError(sql))
	}

	return affected, nil
}

// extractScalarValue 从 map[string]any 中提取第一个值并转换为目标类型
// 默认忽略列名，直接取第一列的值
func (db *Db) extractScalarValue(rowData map[string]any, defaultValue any) any {
	if len(rowData) == 0 {
		return defaultValue
	}

	// 取第一个值（map 的遍历顺序是随机的，但通常数据库返回的顺序是稳定的）
	var rawValue any
	for _, v := range rowData {
		rawValue = v
		break
	}

	if rawValue == nil {
		return defaultValue
	}

	// 推断目标类型并转换
	targetType := reflect.TypeOf(defaultValue)
	convertedValue, err := db.convertToPrimitiveType(rawValue, targetType)
	if err != nil {
		LogError("转换标量值失败: %s, 目标类型=%s", safeErrorForLog(err), targetType)
		return defaultValue
	}

	return convertedValue
}
