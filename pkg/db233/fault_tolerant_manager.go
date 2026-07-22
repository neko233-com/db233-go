package db233

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

var (
	// ErrFaultTolerantManagerStopped 表示容错管理器已进入终止状态。
	ErrFaultTolerantManagerStopped = errors.New("容错管理器已停止")
	// ErrFaultTolerantManagerPathInUse 表示另一实例或进程已独占同一失败队列目录。
	ErrFaultTolerantManagerPathInUse = errors.New("容错持久化路径已被占用")
	safePersistedErrorSummaryPattern = regexp.MustCompile(`^ErrorType=[^,\r\n]{1,256}, ErrorClass=[a-z_]{1,64}$`)
)

// FailedOperation keeps a failed write for retry.
type FailedOperation struct {
	ID                 string         `json:"id"`
	Operation          string         `json:"operation"` // "Save", "Update", "Delete", "SaveBatchUpsert", "ExecuteUpdate"
	SQL                string         `json:"sql"`
	Params             []any          `json:"params"`
	EntityData         map[string]any `json:"entity_data,omitempty"`
	EntityTypeName     string         `json:"entityTypeName,omitempty"`
	EntityJSON         []byte         `json:"entityJSON,omitempty"`
	TableName          string         `json:"table_name"`
	PrimaryKey         any            `json:"primary_key,omitempty"`
	Timestamp          time.Time      `json:"timestamp"`
	RetryCount         int            `json:"retry_count"`
	LastError          string         `json:"last_error,omitempty"`
	DatabaseGeneration string         `json:"database_generation,omitempty"`
}

// FaultTolerantManager provides health probing and at-least-once retry.
//
// Save/Update/UPSERT 可依靠业务主键安全重放。ExecuteUpdate 若服务端已提交、客户端却收到
// 连接错误，可能重复执行；调用方必须使用幂等 SQL 或业务幂等键。本组件不提供分布式
// exactly-once 保证，也不会关闭或替换 Db.DataSource；database/sql 自身负责连接池重建。
type FaultTolerantManager struct {
	db             *Db
	dbConfig       *DbConnectionConfig
	reconnectMutex sync.RWMutex
	isReconnecting bool
	lastReconnect  time.Time

	failedOps          []*FailedOperation
	failedOpsMutex     sync.RWMutex
	retryMutex         sync.Mutex
	persistPath        string
	recoveryMu         sync.Mutex
	pathLock           *flock.Flock
	pathLockPath       string
	databaseGeneration string
	generationErr      error
	recoveryLoaded     bool

	maxReconnectAttempts int
	reconnectInterval    time.Duration
	healthCheckInterval  time.Duration
	healthCheckTimeout   time.Duration

	maxRetryAttempts   int
	retryInterval      time.Duration
	neverDropFailedOps bool

	stopChan           chan struct{}
	healthCheckTicker  *time.Ticker
	retryTicker        *time.Ticker
	lifecycleMu        sync.Mutex
	loopWG             sync.WaitGroup
	operationWG        sync.WaitGroup
	started            bool
	stopped            bool
	reconnectScheduled bool
	stopOnce           sync.Once
	stopErr            error
	lifecycleCtx       context.Context
	cancelLifecycle    context.CancelFunc
}

// NewFaultTolerantManager creates a manager with defaults.
//
// 默认持久化目录是进程工作目录下的 .db233_failed_ops，并由单个 manager 独占。
// 同一进程存在多个 Db 时必须为每个 manager 配置唯一目录；误用同目录会返回
// ErrFaultTolerantManagerPathInUse，而不会覆盖另一实例的失败队列。
func NewFaultTolerantManager(db *Db, dbConfig *DbConnectionConfig) *FaultTolerantManager {
	persistPath := filepath.Join(".", ".db233_failed_ops")
	if absolutePath, err := filepath.Abs(persistPath); err == nil {
		persistPath = absolutePath
	}
	lifecycleCtx, cancelLifecycle := context.WithCancel(context.Background())
	ftm := &FaultTolerantManager{
		db:                   db,
		dbConfig:             dbConfig,
		failedOps:            make([]*FailedOperation, 0),
		persistPath:          persistPath,
		maxReconnectAttempts: 10,
		reconnectInterval:    5 * time.Second,
		healthCheckInterval:  30 * time.Second,
		healthCheckTimeout:   5 * time.Second,
		maxRetryAttempts:     0, // 0 = 无限重试，游戏数据绝不丢弃
		retryInterval:        10 * time.Second,
		neverDropFailedOps:   true,
		stopChan:             make(chan struct{}),
		lifecycleCtx:         lifecycleCtx,
		cancelLifecycle:      cancelLifecycle,
	}

	return ftm
}

// SetNeverDropFailedOps 设置是否永不丢弃失败操作（游戏服默认 true）。
func (ftm *FaultTolerantManager) SetNeverDropFailedOps(neverDrop bool) {
	ftm.failedOpsMutex.Lock()
	defer ftm.failedOpsMutex.Unlock()
	ftm.neverDropFailedOps = neverDrop
}

// RetryFailedOperationsNow 立即重试失败操作。
//
// 兼容旧 API：历史版本不返回 error。需要 all-or-error 语义的新代码应调用
// RetryFailedOperationsNowStrict。
func (ftm *FaultTolerantManager) RetryFailedOperationsNow() {
	if err := ftm.RetryFailedOperationsNowStrict(); err != nil {
		LogError("立即重试失败操作未完成: %s", safeErrorForLog(err))
	}
}

// RetryFailedOperationsNowStrict 立即重试失败操作并传播全部错误。
func (ftm *FaultTolerantManager) RetryFailedOperationsNowStrict() error {
	return ftm.retryFailedOperations()
}

// SetPersistPath sets the path for persistence.
func (ftm *FaultTolerantManager) SetPersistPath(path string) {
	if err := ftm.SetPersistPathStrict(path); err != nil {
		LogError("修改容错持久化目录失败: %s", safeErrorForLog(err))
	}
}

// SetPersistPathStrict sets the persistence path and propagates lifecycle errors.
func (ftm *FaultTolerantManager) SetPersistPathStrict(path string) error {
	if ftm == nil {
		return NewValidationException("容错管理器不能为 nil")
	}
	if path == "" {
		return NewValidationException("持久化目录不能为空")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return NewConfigurationExceptionWithCause(err, "无法解析容错持久化目录")
	}
	path = absolutePath
	ftm.lifecycleMu.Lock()
	defer ftm.lifecycleMu.Unlock()
	if ftm.started || ftm.stopped {
		return NewValidationException("容错管理器启动或停止后禁止修改持久化目录")
	}
	ftm.recoveryMu.Lock()
	defer ftm.recoveryMu.Unlock()
	ftm.failedOpsMutex.Lock()
	defer ftm.failedOpsMutex.Unlock()
	if path == ftm.persistPath {
		return nil
	}
	if ftm.pathLock != nil || ftm.recoveryLoaded {
		return NewValidationException("容错恢复初始化后禁止修改持久化目录")
	}
	ftm.persistPath = path
	ftm.recoveryLoaded = false
	ftm.generationErr = nil
	return nil
}

// ConfigureDatabaseGeneration 在加载失败操作前绑定数据库代次。
// 空值仅用于向后兼容；生产环境必须配置非空 generation。
func (ftm *FaultTolerantManager) ConfigureDatabaseGeneration(generation string) error {
	if ftm == nil {
		return nil
	}
	ftm.lifecycleMu.Lock()
	defer ftm.lifecycleMu.Unlock()
	if ftm.stopped {
		return ErrFaultTolerantManagerStopped
	}
	if ftm.started {
		return NewValidationException("容错管理器启动后请使用 RotateDatabaseGeneration")
	}
	ftm.recoveryMu.Lock()
	defer ftm.recoveryMu.Unlock()
	ftm.failedOpsMutex.Lock()
	defer ftm.failedOpsMutex.Unlock()
	ftm.failedOps = nil
	ftm.databaseGeneration = generation
	ftm.recoveryLoaded = false
	ftm.generationErr = nil
	return ftm.initializeRecoveryLocked()
}

// RotateDatabaseGeneration 清除内存失败队列、隔离磁盘队列并切换代次。
func (ftm *FaultTolerantManager) RotateDatabaseGeneration(generation string) error {
	if ftm == nil {
		return nil
	}
	if generation == "" {
		return NewValidationException("DatabaseGeneration 不能为空")
	}
	// 已绑定 Db 时必须轮换全部恢复/Session/写缓冲组件，
	// 禁止单独清空 FTM 队列造成代次分裂。
	if ftm.db != nil {
		db := ftm.db
		db.resourceMu.Lock()
		attached := db.FaultTolerantMgr == ftm
		db.resourceMu.Unlock()
		if !attached {
			return NewValidationException("已绑定 Db 但尚未挂载的容错管理器禁止直接轮换；初始化阶段请使用 ConfigureDatabaseGeneration")
		}
		return db.RotateDatabaseGeneration(generation)
	}
	return ftm.rotateDatabaseGenerationUnderBarrier(generation)
}

// rotateDatabaseGenerationUnderBarrier 仅供 Db transition 在独占屏障内调用。
func (ftm *FaultTolerantManager) rotateDatabaseGenerationUnderBarrier(generation string) error {
	if release, err := ftm.beginOperation(); err != nil {
		return err
	} else {
		defer release()
	}
	ftm.recoveryMu.Lock()
	defer ftm.recoveryMu.Unlock()
	ftm.failedOpsMutex.Lock()
	defer ftm.failedOpsMutex.Unlock()
	if err := ftm.ensurePathOwnershipLocked(); err != nil {
		ftm.generationErr = errors.Join(ErrDatabaseGenerationBlocked, err)
		return ftm.generationErr
	}
	if ftm.databaseGeneration == generation && ftm.generationErr == nil && ftm.recoveryLoaded {
		return nil
	}
	ftm.failedOps = nil
	ftm.generationErr = ErrDatabaseGenerationBlocked
	if err := prepareRecoveryGeneration(
		ftm.persistPath,
		"failed-ops-generation.json",
		[]string{ftm.failedOperationsFileLocked()},
		"failed-ops",
		generation,
		true,
	); err != nil {
		ftm.generationErr = errors.Join(ErrDatabaseGenerationBlocked, err)
		return ftm.generationErr
	}
	ftm.databaseGeneration = generation
	ftm.generationErr = nil
	ftm.recoveryLoaded = true
	return nil
}

// Start launches health check and retry loops using the legacy void contract.
func (ftm *FaultTolerantManager) Start() {
	if err := ftm.StartStrict(); err != nil {
		LogError("容错管理器启动失败: %s", safeErrorForLog(err))
	}
}

// StartStrict launches health check and retry loops and returns initialization errors.
func (ftm *FaultTolerantManager) StartStrict() error {
	if ftm == nil {
		return NewValidationException("容错管理器不能为 nil")
	}
	ftm.lifecycleMu.Lock()
	if ftm.stopped {
		ftm.lifecycleMu.Unlock()
		return ErrFaultTolerantManagerStopped
	}
	if ftm.started {
		ftm.lifecycleMu.Unlock()
		return nil
	}
	if err := ftm.ensureRecoveryInitialized(); err != nil {
		ftm.lifecycleMu.Unlock()
		return err
	}
	LogInfo("容错管理器启动: 健康检查间隔=%v, 重试间隔=%v", ftm.healthCheckInterval, ftm.retryInterval)
	healthInterval := ftm.healthCheckInterval
	if healthInterval <= 0 {
		healthInterval = 30 * time.Second
	}
	retryInterval := ftm.retryInterval
	if retryInterval <= 0 {
		retryInterval = 10 * time.Second
	}
	ftm.healthCheckTicker = time.NewTicker(healthInterval)
	ftm.retryTicker = time.NewTicker(retryInterval)
	ftm.started = true
	ftm.loopWG.Add(2)
	go ftm.healthCheckLoop()
	go ftm.retryLoop()
	ftm.lifecycleMu.Unlock()
	return nil
}

// Stop stops loops and persists failed operations using the legacy void contract.
func (ftm *FaultTolerantManager) Stop() {
	if err := ftm.StopStrict(); err != nil {
		LogError("容错管理器停止失败: %s", safeErrorForLog(err))
	}
}

// StopStrict cancels all background work, waits for in-flight DB access, then persists.
// It is idempotent and guarantees that no manager-owned goroutine accesses the DB after return.
func (ftm *FaultTolerantManager) StopStrict() error {
	if ftm == nil {
		return nil
	}
	ftm.stopOnce.Do(func() {
		LogInfo("容错管理器停止")
		ftm.lifecycleMu.Lock()
		ftm.stopped = true
		started := ftm.started
		ftm.cancelLifecycle()
		if ftm.healthCheckTicker != nil {
			ftm.healthCheckTicker.Stop()
		}
		if ftm.retryTicker != nil {
			ftm.retryTicker.Stop()
		}
		close(ftm.stopChan)
		ftm.lifecycleMu.Unlock()
		if started {
			ftm.loopWG.Wait()
		}
		ftm.operationWG.Wait()
		ftm.stopErr = ftm.persistAndReleasePathOwnership()
	})
	return ftm.stopErr
}

func (ftm *FaultTolerantManager) healthCheckLoop() {
	defer ftm.loopWG.Done()
	for {
		select {
		case <-ftm.healthCheckTicker.C:
			if err := ftm.checkAndReconnect(); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrFaultTolerantManagerStopped) {
				LogWarn("数据库健康检查未恢复: %s", safeErrorForLog(err))
			}
		case <-ftm.stopChan:
			return
		}
	}
}

func (ftm *FaultTolerantManager) retryLoop() {
	defer ftm.loopWG.Done()
	for {
		select {
		case <-ftm.retryTicker.C:
			if err := ftm.retryFailedOperations(); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrFaultTolerantManagerStopped) {
				LogWarn("后台重试未完成: %s", safeErrorForLog(err))
			}
		case <-ftm.stopChan:
			return
		}
	}
}

// CheckAndReconnect schedules a coalesced health recovery task and returns immediately.
// The task is owned by the manager lifecycle and StopStrict waits for it.
func (ftm *FaultTolerantManager) CheckAndReconnect() {
	if ftm == nil {
		return
	}
	ftm.lifecycleMu.Lock()
	if ftm.stopped || ftm.reconnectScheduled {
		ftm.lifecycleMu.Unlock()
		return
	}
	ftm.reconnectScheduled = true
	ftm.operationWG.Add(1)
	ftm.lifecycleMu.Unlock()
	go func() {
		defer ftm.operationWG.Done()
		defer func() {
			ftm.lifecycleMu.Lock()
			ftm.reconnectScheduled = false
			ftm.lifecycleMu.Unlock()
		}()
		if err := ftm.checkAndReconnectContext(ftm.lifecycleCtx); err != nil && !errors.Is(err, context.Canceled) {
			LogWarn("数据库健康恢复未完成: %s", safeErrorForLog(err))
		}
	}()
}

// CheckAndReconnectStrict probes the existing sql.DB pool and propagates recovery errors.
func (ftm *FaultTolerantManager) CheckAndReconnectStrict() error {
	if ftm == nil {
		return NewValidationException("容错管理器不能为 nil")
	}
	return ftm.checkAndReconnect()
}

func (ftm *FaultTolerantManager) checkAndReconnect() error {
	release, err := ftm.beginOperation()
	if err != nil {
		return err
	}
	defer release()
	return ftm.checkAndReconnectContext(ftm.lifecycleCtx)
}

func (ftm *FaultTolerantManager) checkAndReconnectContext(ctx context.Context) error {
	ftm.reconnectMutex.Lock()
	defer ftm.reconnectMutex.Unlock()

	if ftm.isReconnecting {
		return nil
	}

	if err := ftm.pingContext(ctx); err != nil {
		LogWarn("数据库连接不健康，开始恢复现有连接池: %s", safeErrorForLog(err))
		return ftm.reconnectContext(ctx)
	}
	return nil
}

func (ftm *FaultTolerantManager) reconnectContext(ctx context.Context) error {
	if ftm.isReconnecting {
		return nil
	}

	if wait := ftm.reconnectInterval - time.Since(ftm.lastReconnect); wait > 0 {
		if err := waitForRecoveryBackoff(ctx, wait); err != nil {
			return err
		}
	}

	ftm.isReconnecting = true
	defer func() {
		ftm.isReconnecting = false
		ftm.lastReconnect = time.Now()
	}()

	LogInfo("开始恢复数据库连接池...")

	attempts := ftm.maxReconnectAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ftm.pingContext(ctx); err != nil {
			lastErr = err
			LogWarn("重连尝试 %d/%d 失败: %s", attempt, attempts, safeErrorForLog(err))
			if attempt < attempts {
				if err := waitForRecoveryBackoff(ctx, ftm.reconnectInterval); err != nil {
					return err
				}
			}
			continue
		}

		LogInfo("数据库连接池恢复成功(尝试 %d/%d)", attempt, attempts)
		return nil
	}

	return fmt.Errorf("数据库连接池恢复失败，已尝试 %d 次: %w", attempts, lastErr)
}

// RecordFailedOperation adds a failed write.
//
// 兼容旧 API：历史版本不返回 error。严格写路径应调用
// RecordFailedOperationStrict，并将持久化错误合并回原始写错误。
func (ftm *FaultTolerantManager) RecordFailedOperation(op *FailedOperation) {
	if err := ftm.RecordFailedOperationStrict(op); err != nil {
		LogError("失败操作未能持久化: %s", safeErrorForLog(err))
	}
}

// RecordFailedOperationStrict 记录失败写入，并传播 generation/持久化错误。
func (ftm *FaultTolerantManager) RecordFailedOperationStrict(op *FailedOperation) error {
	if ftm == nil || op == nil {
		return NewValidationException("失败操作不能为空")
	}
	lockedGeneration := ""
	releaseGeneration := func() {}
	if ftm.db != nil {
		current, release, err := ftm.db.lockCurrentDatabaseGeneration()
		if err != nil {
			return err
		}
		lockedGeneration = current
		releaseGeneration = release
	}
	defer releaseGeneration()
	return ftm.recordFailedOperationUnderGenerationLease(op, lockedGeneration)
}

// recordFailedOperationUnderGenerationLease 供已持有 Db generation 读租约的写路径调用，
// 避免 RWMutex 在有等待写者时发生嵌套读锁死锁。
func (ftm *FaultTolerantManager) recordFailedOperationUnderGenerationLease(op *FailedOperation, expectedGeneration string) error {
	if ftm == nil || op == nil {
		return NewValidationException("失败操作不能为空")
	}
	releaseOperation, err := ftm.beginOperation()
	if err != nil {
		return err
	}
	defer releaseOperation()
	cloned := cloneFailedOperation(op)
	opaqueID, idErr := newFailedOperationID()
	if idErr != nil {
		return NewDb233ExceptionWithCause(idErr, "生成失败操作 ID 失败")
	}
	now := time.Now()
	ftm.recoveryMu.Lock()
	defer ftm.recoveryMu.Unlock()
	ftm.failedOpsMutex.Lock()
	defer ftm.failedOpsMutex.Unlock()
	if err := ftm.initializeRecoveryLocked(); err != nil {
		return err
	}
	if ftm.generationErr != nil {
		return ftm.generationErr
	}
	if ftm.db != nil && ftm.databaseGeneration != expectedGeneration {
		return fmt.Errorf("%w: 失败操作=%s, Db=%s", ErrDatabaseGenerationChanged, safeValueForLog(ftm.databaseGeneration), safeValueForLog(expectedGeneration))
	}

	cloned.ID = opaqueID
	cloned.Timestamp = now
	cloned.RetryCount = 0
	cloned.DatabaseGeneration = ftm.databaseGeneration
	if cloned.LastError != "" {
		// LastError 是诊断元数据，不是恢复载荷。公共 API 传入的原始文本可能
		// 包含 SQL 参数或控制字符，只持久化不可逆摘要。
		cloned.LastError = safeErrorSummary(errors.New(cloned.LastError))
	}

	ftm.failedOps = append(ftm.failedOps, cloned)
	if err := ftm.persistFailedOperationsLocked(); err != nil {
		return err
	}

	LogWarn(
		"记录失败操作: ID=%s, Operation=%s, Table=%s",
		safeValueForLog(cloned.ID),
		safeValueForLog(cloned.Operation),
		safeValueForLog(cloned.TableName),
	)
	return nil
}

func newFailedOperationID() (string, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return "ftm_" + hex.EncodeToString(entropy[:]), nil
}

func (ftm *FaultTolerantManager) retryFailedOperations() error {
	if ftm == nil {
		return NewValidationException("容错管理器不能为 nil")
	}
	releaseOperation, err := ftm.beginOperation()
	if err != nil {
		return err
	}
	defer releaseOperation()
	return ftm.retryFailedOperationsContext(ftm.lifecycleCtx)
}

func (ftm *FaultTolerantManager) retryFailedOperationsContext(ctx context.Context) error {
	return ftm.retryFailedOperationsContextWithGeneration(ctx, "", false)
}

// drainUnderGenerationLeaseStrict 严格重试并确认失败队列已清空。
// 调用方必须持续持有 Db generation 的读锁或写锁；本方法不会嵌套获取该锁。
func (ftm *FaultTolerantManager) drainUnderGenerationLeaseStrict(expectedGeneration string) (remaining int, drainErr error) {
	if ftm == nil {
		return 0, NewValidationException("容错管理器不能为 nil")
	}
	releaseOperation, err := ftm.beginOperation()
	if err != nil {
		return 0, err
	}
	defer releaseOperation()

	retryErr := ftm.retryFailedOperationsContextWithGeneration(ftm.lifecycleCtx, expectedGeneration, true)
	remaining, countErr := ftm.failedOperationCountWithLifecycleLeaseStrict(expectedGeneration)
	drainErr = errors.Join(retryErr, countErr)
	if countErr == nil && remaining > 0 {
		drainErr = errors.Join(
			drainErr,
			NewQueryException(fmt.Sprintf("失败操作排空后仍有 %d 条待重试", remaining)),
		)
	}
	return remaining, drainErr
}

// failedOperationCountWithLifecycleLeaseStrict 要求调用方已登记 lifecycle operation，且
// 持有 Db generation 租约。它同时验证持久化队列与租约属于同一代次。
func (ftm *FaultTolerantManager) failedOperationCountWithLifecycleLeaseStrict(expectedGeneration string) (int, error) {
	ftm.recoveryMu.Lock()
	defer ftm.recoveryMu.Unlock()
	ftm.failedOpsMutex.Lock()
	defer ftm.failedOpsMutex.Unlock()
	if err := ftm.initializeRecoveryLocked(); err != nil {
		return 0, err
	}
	if ftm.generationErr != nil {
		return 0, ftm.generationErr
	}
	if ftm.db != nil && ftm.databaseGeneration != expectedGeneration {
		return 0, fmt.Errorf("%w: 失败操作=%s, Db=%s", ErrDatabaseGenerationChanged, safeValueForLog(ftm.databaseGeneration), safeValueForLog(expectedGeneration))
	}
	return len(ftm.failedOps), nil
}

func (ftm *FaultTolerantManager) retryFailedOperationsContextWithGeneration(
	ctx context.Context,
	expectedGeneration string,
	generationLeaseHeld bool,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ftm.retryMutex.Lock()
	defer ftm.retryMutex.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	// 网络恢复/退避不持有 generation 或失败队列锁，避免一次断网阻塞清库屏障
	// 和新的失败写入。真正回放时再获取 generation 读租约。
	ftm.recoveryMu.Lock()
	ftm.failedOpsMutex.Lock()
	if err := ftm.initializeRecoveryLocked(); err != nil {
		ftm.failedOpsMutex.Unlock()
		ftm.recoveryMu.Unlock()
		return err
	}
	if ftm.generationErr != nil {
		err := ftm.generationErr
		ftm.failedOpsMutex.Unlock()
		ftm.recoveryMu.Unlock()
		return err
	}
	hasFailedOperations := len(ftm.failedOps) > 0
	ftm.failedOpsMutex.Unlock()
	ftm.recoveryMu.Unlock()
	if !hasFailedOperations {
		return nil
	}
	if err := ftm.pingContext(ctx); err != nil {
		LogDebug("连接不健康，尝试恢复后再重试: %s", safeErrorForLog(err))
		ftm.reconnectMutex.Lock()
		reconnectErr := ftm.reconnectContext(ctx)
		ftm.reconnectMutex.Unlock()
		if reconnectErr != nil {
			return reconnectErr
		}
	}

	releaseGeneration := func() {}
	lockedGeneration := expectedGeneration
	if ftm.db != nil && !generationLeaseHeld {
		current, release, err := ftm.db.lockCurrentDatabaseGeneration()
		if err != nil {
			return err
		}
		lockedGeneration = current
		releaseGeneration = release
	}
	defer releaseGeneration()
	ftm.recoveryMu.Lock()
	defer ftm.recoveryMu.Unlock()
	ftm.failedOpsMutex.Lock()
	defer ftm.failedOpsMutex.Unlock()
	if err := ftm.initializeRecoveryLocked(); err != nil {
		return err
	}
	if ftm.generationErr != nil {
		return ftm.generationErr
	}
	if ftm.db != nil && ftm.databaseGeneration != lockedGeneration {
		return fmt.Errorf("%w: 失败操作=%s, Db=%s", ErrDatabaseGenerationChanged, safeValueForLog(ftm.databaseGeneration), safeValueForLog(lockedGeneration))
	}
	if len(ftm.failedOps) == 0 {
		return nil
	}

	LogInfo("开始重试失败操作, 总数=%d", len(ftm.failedOps))
	remainingOps := make([]*FailedOperation, 0, len(ftm.failedOps))
	retryErrors := make([]error, 0, 8)
	suppressedErrors := 0
	for index, op := range ftm.failedOps {
		if err := ctx.Err(); err != nil {
			remainingOps = append(remainingOps, ftm.failedOps[index:]...)
			retryErrors = appendBoundedRecoveryError(retryErrors, err, &suppressedErrors)
			break
		}
		if op == nil {
			retryErrors = appendBoundedRecoveryError(retryErrors, errors.New("失败操作队列包含 nil"), &suppressedErrors)
			continue
		}
		if !ftm.neverDropFailedOps && ftm.maxRetryAttempts > 0 && op.RetryCount >= ftm.maxRetryAttempts {
			LogError(
				"操作重试次数已达上限，放弃: ID=%s, Operation=%s",
				safeValueForLog(op.ID),
				safeValueForLog(op.Operation),
			)
			retryErrors = appendBoundedRecoveryError(
				retryErrors,
				NewQueryException(fmt.Sprintf("失败操作已达到重试上限: ID=%s", safeValueForLog(op.ID))),
				&suppressedErrors,
			)
			continue
		}

		op.RetryCount++
		executeErr := ftm.executeFailedOperation(ctx, op, lockedGeneration)
		if executeErr == nil {
			LogInfo(
				"失败操作重试成功: ID=%s, Operation=%s, 重试次数=%d",
				safeValueForLog(op.ID),
				safeValueForLog(op.Operation),
				op.RetryCount,
			)
			continue
		}
		op.LastError = safeErrorSummary(executeErr)
		remainingOps = append(remainingOps, op)
		retryErrors = appendBoundedRecoveryError(
			retryErrors,
			NewQueryExceptionWithCause(
				executeErr,
				fmt.Sprintf(
					"重试失败操作未完成: ID=%s, Operation=%s",
					safeValueForLog(op.ID),
					safeValueForLog(op.Operation),
				),
			),
			&suppressedErrors,
		)
		LogWarn(
			"失败操作重试失败: ID=%s, Operation=%s, 重试次数=%d, err=%s",
			safeValueForLog(op.ID),
			safeValueForLog(op.Operation),
			op.RetryCount,
			safeErrorForLog(executeErr),
		)
	}

	ftm.failedOps = remainingOps
	persistErr := ftm.persistFailedOperationsLocked()
	if suppressedErrors > 0 {
		retryErrors = append(retryErrors, fmt.Errorf("另有 %d 个重试错误已省略", suppressedErrors))
	}
	return errors.Join(errors.Join(retryErrors...), persistErr)
}

func (ftm *FaultTolerantManager) executeFailedOperation(ctx context.Context, op *FailedOperation, databaseGeneration string) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			LogError(
				"执行失败操作时发生 panic: %s, Operation=%s",
				safeValueForLog(recovered),
				safeValueForLog(op.Operation),
			)
			message := fmt.Sprintf(
				"执行失败操作 panic: Value=%s, Operation=%s",
				safeValueForLog(recovered),
				safeValueForLog(op.Operation),
			)
			if cause, ok := recovered.(error); ok {
				err = NewQueryExceptionWithCause(cause, message)
			} else {
				err = NewQueryException(message)
			}
		}
	}()

	switch op.Operation {
	case "Save", "Update", "SaveBatchUpsert":
		return ftm.executeSaveOrUpdate(ctx, op, databaseGeneration)
	case "Delete":
		return ftm.executeDelete(ctx, op)
	case "ExecuteUpdate":
		return ftm.executeUpdate(ctx, op, databaseGeneration)
	default:
		return NewQueryException(fmt.Sprintf("未知的操作类型: %s", safeValueForLog(op.Operation)))
	}
}

func (ftm *FaultTolerantManager) executeSaveOrUpdate(ctx context.Context, op *FailedOperation, databaseGeneration string) error {
	var entityReplayErr error
	// 优先用实体 JSON 回放（比 SQL 参数更可靠）。Db.Close 会等待本次重试完成。
	if len(op.EntityJSON) > 0 && op.EntityTypeName != "" {
		entity, err := DeserializeEntity(op.EntityTypeName, op.EntityJSON)
		if err != nil {
			entityReplayErr = err
		} else {
			repo := NewBaseCrudRepository(ftm.db)
			entityReplayErr = repo.replayBatchUpsertOnceUnderGenerationLease([]IDbEntity{entity}, databaseGeneration)
			if entityReplayErr == nil {
				return nil
			}
		}
	}

	if len(op.Params) == 0 {
		if entityReplayErr != nil {
			return NewQueryExceptionWithCause(
				entityReplayErr,
				fmt.Sprintf("实体回放失败且无 SQL 参数可回退: ID=%s", safeValueForLog(op.ID)),
			)
		}
		return NewQueryException(fmt.Sprintf("操作参数为空: ID=%s", safeValueForLog(op.ID)))
	}
	if ftm.db == nil || ftm.db.DataSource == nil {
		return NewQueryException("数据库连接未初始化")
	}
	ftm.db.recordFlushWriteAttempt(FlushWriteSourceFaultToleranceReplay, 0)
	result, err := ftm.db.DataSource.ExecContext(ctx, op.SQL, op.Params...)
	ftm.db.recordFlushWriteResult(FlushWriteSourceFaultToleranceReplay, 0, err == nil)
	if err != nil {
		return errors.Join(entityReplayErr, err)
	}
	rowsAffected, rowsAffectedErr := result.RowsAffected()
	if rowsAffectedErr != nil {
		return fmt.Errorf("读取失败操作影响行数: %w", rowsAffectedErr)
	}
	LogDebug(
		"失败操作执行成功: ID=%s, Operation=%s, 影响行数=%d",
		safeValueForLog(op.ID),
		safeValueForLog(op.Operation),
		rowsAffected,
	)
	return nil
}

func (ftm *FaultTolerantManager) executeDelete(ctx context.Context, op *FailedOperation) error {
	if len(op.Params) == 0 {
		return NewQueryException(fmt.Sprintf("删除操作参数为空: ID=%s", safeValueForLog(op.ID)))
	}
	if ftm.db == nil || ftm.db.DataSource == nil {
		return NewQueryException("数据库连接未初始化")
	}
	ftm.db.recordFlushWriteAttempt(FlushWriteSourceFaultToleranceReplay, 0)
	result, err := ftm.db.DataSource.ExecContext(ctx, op.SQL, op.Params...)
	ftm.db.recordFlushWriteResult(FlushWriteSourceFaultToleranceReplay, 0, err == nil)
	if err != nil {
		return err
	}
	rowsAffected, rowsAffectedErr := result.RowsAffected()
	if rowsAffectedErr != nil {
		return fmt.Errorf("读取删除操作影响行数: %w", rowsAffectedErr)
	}
	LogDebug("删除操作执行成功: ID=%s, 影响行数=%d", safeValueForLog(op.ID), rowsAffected)
	return nil
}

func (ftm *FaultTolerantManager) executeUpdate(ctx context.Context, op *FailedOperation, databaseGeneration string) error {
	return ftm.executeSaveOrUpdate(ctx, op, databaseGeneration)
}

func (ftm *FaultTolerantManager) failedOperationsFileLocked() string {
	return filepath.Join(ftm.persistPath, "failed_operations.json")
}

func (ftm *FaultTolerantManager) ensureRecoveryInitialized() error {
	ftm.recoveryMu.Lock()
	defer ftm.recoveryMu.Unlock()
	ftm.failedOpsMutex.Lock()
	defer ftm.failedOpsMutex.Unlock()
	return ftm.initializeRecoveryLocked()
}

// initializeRecoveryLocked 要求 recoveryMu 与 failedOpsMutex 均已持有。
func (ftm *FaultTolerantManager) initializeRecoveryLocked() error {
	if ftm.recoveryLoaded {
		return ftm.generationErr
	}
	if ftm.persistPath == "" {
		ftm.recoveryLoaded = true
		return nil
	}
	if err := ftm.ensurePathOwnershipLocked(); err != nil {
		ftm.generationErr = errors.Join(ErrDatabaseGenerationBlocked, err)
		ftm.recoveryLoaded = true
		return ftm.generationErr
	}
	if ftm.databaseGeneration != "" {
		ftm.generationErr = ErrDatabaseGenerationBlocked
		if err := prepareRecoveryGeneration(
			ftm.persistPath,
			"failed-ops-generation.json",
			[]string{ftm.failedOperationsFileLocked()},
			"failed-ops",
			ftm.databaseGeneration,
			false,
		); err != nil {
			ftm.generationErr = errors.Join(ErrDatabaseGenerationBlocked, err)
			ftm.recoveryLoaded = true
			return ftm.generationErr
		}
	}
	if err := ftm.loadFailedOperationsLocked(); err != nil {
		ftm.generationErr = errors.Join(ErrDatabaseGenerationBlocked, err)
		ftm.recoveryLoaded = true
		return ftm.generationErr
	}
	ftm.generationErr = nil
	ftm.recoveryLoaded = true
	return nil
}

func (ftm *FaultTolerantManager) persistFailedOperations() error {
	ftm.recoveryMu.Lock()
	defer ftm.recoveryMu.Unlock()
	ftm.failedOpsMutex.Lock()
	defer ftm.failedOpsMutex.Unlock()
	if err := ftm.initializeRecoveryLocked(); err != nil {
		return err
	}
	return ftm.persistFailedOperationsLocked()
}

// persistAndReleasePathOwnership 只在已成功持有独占锁时落盘。
// 初始化因路径冲突失败的 manager 在 Stop 时绝不能随后抢锁并覆盖现有队列。
func (ftm *FaultTolerantManager) persistAndReleasePathOwnership() error {
	ftm.recoveryMu.Lock()
	defer ftm.recoveryMu.Unlock()
	ftm.failedOpsMutex.Lock()
	defer ftm.failedOpsMutex.Unlock()

	var persistErr error
	if ftm.pathLock != nil {
		persistErr = ftm.persistFailedOperationsLocked()
	} else if ftm.generationErr != nil {
		persistErr = ftm.generationErr
	}
	releaseErr := ftm.releasePathOwnershipLocked()
	return errors.Join(persistErr, releaseErr)
}

// persistFailedOperationsLocked 要求 failedOpsMutex 已持有。
func (ftm *FaultTolerantManager) persistFailedOperationsLocked() error {
	if ftm.persistPath == "" {
		return nil
	}
	if err := ftm.ensurePathOwnershipLocked(); err != nil {
		return errors.Join(ErrDatabaseGenerationBlocked, err)
	}
	if ftm.generationErr != nil {
		return ftm.generationErr
	}

	if err := ensurePrivateRecoveryDirectory(ftm.persistPath); err != nil {
		return fmt.Errorf("创建持久化目录: %w", err)
	}

	for _, op := range ftm.failedOps {
		if op != nil {
			op.DatabaseGeneration = ftm.databaseGeneration
		}
	}
	filePath := ftm.failedOperationsFileLocked()
	if err := writeJSONAtomic(filePath, ftm.failedOps, recoveryFileMode); err != nil {
		return fmt.Errorf("写入持久化文件: %w", err)
	}

	LogDebug("失败操作已持久化: 文件=%s, 数量=%d", safeValueForLog(filePath), len(ftm.failedOps))
	return nil
}

// loadFailedOperationsLocked 要求 failedOpsMutex 已持有。
func (ftm *FaultTolerantManager) loadFailedOperationsLocked() error {
	if ftm.persistPath == "" {
		return nil
	}

	filePath := ftm.failedOperationsFileLocked()
	if err := ensurePrivateRecoveryFileIfExists(filePath); err != nil {
		return fmt.Errorf("收紧持久化文件权限: %w", err)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			LogDebug("持久化文件不存在，跳过加载: %s", safeValueForLog(filePath))
			ftm.failedOps = nil
			return nil
		}
		return fmt.Errorf("读取持久化文件: %w", err)
	}

	var ops []*FailedOperation
	if err := json.Unmarshal(data, &ops); err != nil {
		if quarantineErr := quarantineRecoveryFile(ftm.persistPath, filePath, "failed-ops-corrupt"); quarantineErr != nil {
			return fmt.Errorf("失败操作损坏且隔离失败: parse=%v, quarantine=%w", err, quarantineErr)
		}
		return fmt.Errorf("失败操作损坏，已隔离: %w", err)
	}
	if ftm.databaseGeneration != "" {
		for _, op := range ops {
			if op == nil || op.DatabaseGeneration != ftm.databaseGeneration {
				if quarantineErr := quarantineRecoveryFile(ftm.persistPath, filePath, "failed-ops-entry-mismatch"); quarantineErr != nil {
					return fmt.Errorf("失败操作 generation 不匹配且隔离失败: %w", quarantineErr)
				}
				ftm.failedOps = nil
				return fmt.Errorf("%w: 失败操作 generation 不匹配，旧队列已隔离", ErrDatabaseGenerationBlocked)
			}
		}
	}
	for _, op := range ops {
		if op != nil {
			op.LastError = sanitizePersistedErrorSummary(op.LastError)
		}
	}
	ftm.failedOps = ops

	LogInfo("加载持久化的失败操作: 数量=%d", len(ops))
	return nil
}

// GetFailedOperationCount returns failed operations count.
func (ftm *FaultTolerantManager) GetFailedOperationCount() int {
	ftm.failedOpsMutex.RLock()
	defer ftm.failedOpsMutex.RUnlock()
	return len(ftm.failedOps)
}

// ClearFailedOperations clears all failed operations.
func (ftm *FaultTolerantManager) ClearFailedOperations() {
	if err := ftm.ClearFailedOperationsStrict(); err != nil {
		LogError("清除失败操作失败: %s", safeErrorForLog(err))
	}
}

// ClearFailedOperationsStrict clears all failed operations and propagates persistence errors.
func (ftm *FaultTolerantManager) ClearFailedOperationsStrict() error {
	if ftm == nil {
		return NewValidationException("容错管理器不能为 nil")
	}
	releaseOperation, err := ftm.beginOperation()
	if err != nil {
		return err
	}
	defer releaseOperation()
	ftm.recoveryMu.Lock()
	defer ftm.recoveryMu.Unlock()
	ftm.failedOpsMutex.Lock()
	defer ftm.failedOpsMutex.Unlock()

	if err := ftm.initializeRecoveryLocked(); err != nil {
		return err
	}
	previous := ftm.failedOps
	ftm.failedOps = make([]*FailedOperation, 0)
	if err := ftm.persistFailedOperationsLocked(); err != nil {
		// writeJSONAtomic 只在 rename 成功后替换持久化队列。失败时恢复
		// 内存所有权，保持可重试/fail-closed，不把恢复记录静默丢失。
		ftm.failedOps = previous
		return err
	}
	LogInfo("已清除所有失败操作")
	return nil
}

func (ftm *FaultTolerantManager) beginOperation() (func(), error) {
	ftm.lifecycleMu.Lock()
	defer ftm.lifecycleMu.Unlock()
	if ftm.stopped {
		return func() {}, ErrFaultTolerantManagerStopped
	}
	ftm.operationWG.Add(1)
	return ftm.operationWG.Done, nil
}

// ensurePathOwnershipLocked 要求 recoveryMu 已持有。
func (ftm *FaultTolerantManager) ensurePathOwnershipLocked() error {
	if ftm.persistPath == "" {
		return nil
	}
	expected := filepath.Clean(filepath.Join(ftm.persistPath, "failed_operations.json.lock"))
	if ftm.pathLock != nil {
		if filepath.Clean(ftm.pathLockPath) != expected {
			return fmt.Errorf("%w: 持有锁期间持久化目录发生变化", ErrFaultTolerantManagerPathInUse)
		}
		return nil
	}
	if err := ensurePrivateRecoveryDirectory(ftm.persistPath); err != nil {
		return err
	}
	if err := ensurePrivateRecoveryFileIfExists(expected); err != nil {
		return err
	}
	pathLock := flock.New(expected, flock.SetPermissions(recoveryFileMode))
	locked, err := pathLock.TryLock()
	if err != nil {
		return fmt.Errorf("获取容错队列 advisory lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("%w: %s", ErrFaultTolerantManagerPathInUse, safeValueForLog(expected))
	}
	cleanup := func(primary error) error {
		unlockErr := pathLock.Unlock()
		if unlockErr != nil {
			unlockErr = fmt.Errorf("回滚释放容错队列 advisory lock: %w", unlockErr)
		}
		return errors.Join(primary, unlockErr)
	}
	if err := ensurePrivateRecoveryFileIfExists(expected); err != nil {
		return cleanup(err)
	}
	if err := syncRecoveryDirectory(ftm.persistPath); err != nil {
		return cleanup(err)
	}
	ftm.pathLock = pathLock
	ftm.pathLockPath = expected
	return nil
}

// releasePathOwnershipLocked 要求 recoveryMu 已持有。稳定锁文件不得删除，避免 inode 竞态。
func (ftm *FaultTolerantManager) releasePathOwnershipLocked() error {
	if ftm.pathLock == nil {
		return nil
	}
	if err := ftm.pathLock.Unlock(); err != nil {
		return fmt.Errorf("释放容错队列 advisory lock: %w", err)
	}
	ftm.pathLock = nil
	ftm.pathLockPath = ""
	return nil
}

func (ftm *FaultTolerantManager) pingContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ftm.db == nil || ftm.db.DataSource == nil {
		return NewQueryException("数据库连接未初始化")
	}
	timeout := ftm.healthCheckTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return ftm.db.DataSource.PingContext(pingCtx)
}

func waitForRecoveryBackoff(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func cloneFailedOperation(source *FailedOperation) *FailedOperation {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.EntityJSON = append([]byte(nil), source.EntityJSON...)
	cloned.Params = make([]any, len(source.Params))
	for index, value := range source.Params {
		cloned.Params[index] = cloneRecoveryValue(value)
	}
	if source.EntityData != nil {
		cloned.EntityData = make(map[string]any, len(source.EntityData))
		for key, value := range source.EntityData {
			cloned.EntityData[key] = cloneRecoveryValue(value)
		}
	}
	cloned.PrimaryKey = cloneRecoveryValue(source.PrimaryKey)
	return &cloned
}

func cloneRecoveryValue(value any) any {
	switch typed := value.(type) {
	case []byte:
		return append([]byte(nil), typed...)
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneRecoveryValue(item)
		}
		return cloned
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			cloned[key] = cloneRecoveryValue(item)
		}
		return cloned
	default:
		return value
	}
}

func sanitizePersistedErrorSummary(value string) string {
	if value == "" || safePersistedErrorSummaryPattern.MatchString(value) {
		return value
	}
	return safeErrorSummary(errors.New(value))
}

func appendBoundedRecoveryError(current []error, err error, suppressed *int) []error {
	if err == nil {
		return current
	}
	const maxReportedRecoveryErrors = 16
	if len(current) < maxReportedRecoveryErrors {
		return append(current, err)
	}
	(*suppressed)++
	return current
}
