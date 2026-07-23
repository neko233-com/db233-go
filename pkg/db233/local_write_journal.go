package db233

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofrs/flock"
)

var (
	// ErrLocalWriteJournalStopped 表示 WAL 已停止且不再接受操作。
	ErrLocalWriteJournalStopped = errors.New("本地 WAL 已停止")
	// ErrLocalWriteJournalPathInUse 表示另一实例或进程已占用同一 WAL 路径。
	ErrLocalWriteJournalPathInUse = errors.New("本地 WAL 路径已被占用")
)

const (
	journalLogFormatVersion          = 1
	journalLogKindUpsert             = "upsert"
	journalLogKindDelete             = "delete"
	journalMaxRecordBytes            = 64 << 20
	journalCompactionMinObsolete     = 256
	journalCompactionObsoletePercent = 50
	journalCompactionHardBytes       = 128 << 20
)

// JournalEntry 本地 WAL 条目（数据库不可用时保证数据不丢）。
type JournalEntry struct {
	ID             string    `json:"id"`
	Operation      string    `json:"operation"`
	TableName      string    `json:"tableName"`
	PrimaryKey     string    `json:"primaryKey"`
	EntityTypeName string    `json:"entityTypeName"`
	EntityJSON     []byte    `json:"entityJSON"`
	SQL            string    `json:"sql,omitempty"`
	Params         []any     `json:"params,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	RetryCount     int       `json:"retryCount"`
	LastError      string    `json:"lastError,omitempty"`
	// DatabaseGeneration 防止清库后回放旧数据库代次的数据。
	DatabaseGeneration string `json:"databaseGeneration,omitempty"`
	// EntitySchemaVersion 防止升级后用新 Entity 结构静默解释旧快照。
	EntitySchemaVersion int64 `json:"entitySchemaVersion,omitempty"`
}

type preparedJournalEntity struct {
	tableName      string
	primaryKey     string
	entityTypeName string
	entityJSON     []byte
}

type journalReplayItem struct {
	entry  *JournalEntry
	entity IDbEntity
}

type journalReplayFailure struct {
	entry *JournalEntry
	cause error
}

// journalLogRecord 是 pending.ndjson 的版本化追加记录。Delete 只对 EntryID
// 仍为当前版本时生效，旧回放完成后不会误删并发写入的新状态。
type journalLogRecord struct {
	FormatVersion      int           `json:"_db233WalVersion"`
	Kind               string        `json:"kind"`
	DatabaseGeneration string        `json:"databaseGeneration,omitempty"`
	Entry              *JournalEntry `json:"entry,omitempty"`
	EntryID            string        `json:"entryId,omitempty"`
}

type journalEntityKey struct {
	tableName  string
	primaryKey string
}

// localWriteJournalState 必须由 journalMu 保护。单独放在指针后，保持公开
// LocalWriteJournal 与 v1.0.10 一样属于 comparable 类型。
type localWriteJournalState struct {
	pendingCache       map[journalEntityKey]*JournalEntry
	keyByEntryID       map[string]journalEntityKey
	pendingCacheLoaded bool
	logRecords         uint64
	logBytes           int64
	appendedBytes      uint64
	compactionCount    uint64
	tornTailCount      uint64
}

// LocalWriteJournal 本地预写日志：先落盘再写库，成功后删除；达到上限后转入死信。
//
// WAL 提供 at-least-once，而非分布式 exactly-once。Save/Update/UPSERT 应使用稳定
// 业务主键；任意 ExecuteUpdate 必须由调用方设计为幂等 SQL 或携带业务幂等键。
type LocalWriteJournal struct {
	dir         string
	journalMu   sync.Mutex
	replayMu    sync.Mutex
	lifecycleMu sync.Mutex

	stopCh              chan struct{}
	doneCh              chan struct{}
	intervalChanged     chan struct{}
	repo                *BaseCrudRepository
	interval            time.Duration
	started             bool
	stopped             bool
	stopOnce            sync.Once
	doneOnce            sync.Once
	operationWG         sync.WaitGroup
	stopErr             error
	lastReplayErr       error
	pathLock            *flock.Flock
	pathLockPath        string
	state               *localWriteJournalState
	rewriteCount        atomic.Uint64
	databaseGeneration  string
	generationErr       error
	recoveryMaxAttempts int
}

// NewLocalWriteJournal 创建本地 WAL。
func NewLocalWriteJournal(dir string, repo *BaseCrudRepository) *LocalWriteJournal {
	if dir == "" {
		dir = filepath.Join(".", ".db233_journal")
	}
	if absolutePath, err := filepath.Abs(dir); err == nil {
		dir = absolutePath
	}
	return &LocalWriteJournal{
		dir:                 dir,
		stopCh:              make(chan struct{}),
		doneCh:              make(chan struct{}),
		intervalChanged:     make(chan struct{}, 1),
		repo:                repo,
		interval:            5 * time.Second,
		recoveryMaxAttempts: defaultRecoveryMaxAttempts,
		state:               &localWriteJournalState{},
	}
}

// SetRecoveryPolicy 配置 WAL 有界重试。
// 必须在 StartStrict 前调用；0 次数使用默认值 2。
func (j *LocalWriteJournal) SetRecoveryPolicy(maxAttempts int) {
	if j == nil {
		return
	}
	normalized, err := normalizeRecoveryMaxAttempts(maxAttempts)
	if err != nil {
		LogError("忽略无效 WAL 恢复策略: %s", safeErrorForLog(err))
		return
	}
	j.lifecycleMu.Lock()
	defer j.lifecycleMu.Unlock()
	if j.started {
		LogError("WAL 启动后禁止修改恢复策略")
		return
	}
	j.recoveryMaxAttempts = normalized
}

// ConfigureDatabaseGeneration 在启动回放前绑定数据库代次。
// generation 为空保留历史兼容行为；生产环境必须传入稳定且可轮换的非空值。
func (j *LocalWriteJournal) ConfigureDatabaseGeneration(generation string) error {
	if j == nil {
		return nil
	}
	j.lifecycleMu.Lock()
	defer j.lifecycleMu.Unlock()
	if j.stopped {
		return ErrLocalWriteJournalStopped
	}
	if j.started {
		return NewValidationException("WAL 启动后请使用 RotateDatabaseGeneration")
	}
	j.replayMu.Lock()
	defer j.replayMu.Unlock()
	j.journalMu.Lock()
	defer j.journalMu.Unlock()
	if err := j.ensurePathOwnershipLocked(); err != nil {
		j.generationErr = errors.Join(ErrDatabaseGenerationBlocked, err)
		return j.generationErr
	}
	if generation == "" {
		j.databaseGeneration = ""
		j.generationErr = nil
		j.invalidatePendingCacheLocked()
		return nil
	}
	j.generationErr = ErrDatabaseGenerationBlocked
	if err := prepareRecoveryGeneration(
		j.dir,
		"wal-generation.json",
		j.recoveryFiles(),
		"wal",
		generation,
		false,
	); err != nil {
		j.generationErr = errors.Join(ErrDatabaseGenerationBlocked, err)
		return j.generationErr
	}
	j.databaseGeneration = generation
	j.generationErr = nil
	j.invalidatePendingCacheLocked()
	if err := j.ensurePendingCacheLoadedLocked(); err != nil {
		if j.generationErr == nil {
			j.generationErr = errors.Join(ErrDatabaseGenerationBlocked, err)
		}
		return j.generationErr
	}
	return nil
}

// RotateDatabaseGeneration 隔离旧 WAL，并切换到新数据库代次。
func (j *LocalWriteJournal) RotateDatabaseGeneration(generation string) error {
	if j == nil {
		return nil
	}
	if generation == "" {
		return NewValidationException("DatabaseGeneration 不能为空")
	}
	// 已绑定 Db 的 WAL 不允许单独切代，否则会与
	// Session/WriteBuffer/FTM 分裂。公开调用必须委托完整 Db 屏障。
	if j.repo != nil && j.repo.db != nil {
		db := j.repo.db
		db.resourceMu.Lock()
		attached := db.WriteJournal == j
		db.resourceMu.Unlock()
		if !attached {
			return NewValidationException("已绑定 Db 但尚未挂载的 WAL 禁止直接轮换；初始化阶段请使用 ConfigureDatabaseGeneration")
		}
		return db.RotateDatabaseGeneration(generation)
	}
	return j.rotateDatabaseGenerationUnderBarrier(generation)
}

// rotateDatabaseGenerationUnderBarrier 仅供 Db transition 在独占屏障内调用。
func (j *LocalWriteJournal) rotateDatabaseGenerationUnderBarrier(generation string) error {
	releaseOperation, err := j.beginOperation()
	if err != nil {
		return err
	}
	defer releaseOperation()
	j.replayMu.Lock()
	defer j.replayMu.Unlock()
	j.journalMu.Lock()
	defer j.journalMu.Unlock()
	if err := j.ensurePathOwnershipLocked(); err != nil {
		j.generationErr = errors.Join(ErrDatabaseGenerationBlocked, err)
		return j.generationErr
	}
	if j.databaseGeneration == generation && j.generationErr == nil {
		return nil
	}
	j.generationErr = ErrDatabaseGenerationBlocked
	if err := prepareRecoveryGeneration(
		j.dir,
		"wal-generation.json",
		j.recoveryFiles(),
		"wal",
		generation,
		true,
	); err != nil {
		j.generationErr = errors.Join(ErrDatabaseGenerationBlocked, err)
		return j.generationErr
	}
	j.databaseGeneration = generation
	j.generationErr = nil
	j.invalidatePendingCacheLocked()
	return nil
}

func (j *LocalWriteJournal) ensureGenerationLocked() error {
	if err := j.ensurePathOwnershipLocked(); err != nil {
		j.generationErr = errors.Join(ErrDatabaseGenerationBlocked, err)
		return j.generationErr
	}
	if j.generationErr != nil {
		return j.generationErr
	}
	return nil
}

// SetRetryInterval 设置后台回放间隔。
func (j *LocalWriteJournal) SetRetryInterval(d time.Duration) {
	if j == nil {
		return
	}
	if d <= 0 {
		LogWarn("忽略无效 WAL 重试间隔: %v", d)
		return
	}
	j.lifecycleMu.Lock()
	j.interval = d
	started := j.started && !j.stopped
	j.lifecycleMu.Unlock()
	if started {
		select {
		case j.intervalChanged <- struct{}{}:
		default:
		}
	}
}

// Start 启动后台回放协程（兼容旧 void API）。
func (j *LocalWriteJournal) Start() {
	if err := j.StartStrict(); err != nil {
		LogError("本地 WAL 启动失败: %s", safeErrorForLog(err))
	}
}

// StartStrict 启动后台回放协程，并传播路径冲突和 generation 错误。
func (j *LocalWriteJournal) StartStrict() error {
	if j == nil {
		return NewValidationException("WAL 不能为 nil")
	}
	j.lifecycleMu.Lock()
	if j.stopped {
		j.lifecycleMu.Unlock()
		return ErrLocalWriteJournalStopped
	}
	if j.started {
		j.lifecycleMu.Unlock()
		return nil
	}
	j.journalMu.Lock()
	if err := j.ensureGenerationLocked(); err != nil {
		j.journalMu.Unlock()
		j.lifecycleMu.Unlock()
		return err
	}
	j.journalMu.Unlock()
	j.started = true
	go j.replayLoop()
	j.lifecycleMu.Unlock()
	LogInfo("本地 WAL 已启动: dir=%s", safeValueForLog(j.dir))
	return nil
}

// Stop 停止回放并 fsync 落盘（兼容旧 void API）。
func (j *LocalWriteJournal) Stop() {
	if err := j.StopStrict(); err != nil {
		LogError("本地 WAL 停止失败: %s", safeErrorForLog(err))
	}
}

// StopStrict 停止回放、等待所有在途操作、同步目录并释放独占路径锁。
func (j *LocalWriteJournal) StopStrict() error {
	if j == nil {
		return nil
	}
	j.stopOnce.Do(func() {
		j.lifecycleMu.Lock()
		j.stopped = true
		started := j.started
		if !started {
			j.doneOnce.Do(func() { close(j.doneCh) })
		}
		j.lifecycleMu.Unlock()
		if started {
			close(j.stopCh)
		}
		<-j.doneCh
		j.operationWG.Wait()
		j.journalMu.Lock()
		var syncErr error
		if j.pathLock != nil {
			syncErr = syncRecoveryDirectory(j.dir)
		}
		releaseErr := j.releasePathOwnershipLocked()
		j.journalMu.Unlock()
		j.lifecycleMu.Lock()
		lastReplayErr := j.lastReplayErr
		j.lifecycleMu.Unlock()
		j.stopErr = errors.Join(lastReplayErr, syncErr, releaseErr)
	})
	return j.stopErr
}

func (j *LocalWriteJournal) journalFile() string {
	return filepath.Join(j.dir, "pending.ndjson")
}

func (j *LocalWriteJournal) compactionFile() string {
	return filepath.Join(j.dir, ".pending.ndjson.compact.tmp")
}

func (j *LocalWriteJournal) legacyCompactionFile() string {
	return j.journalFile() + ".tmp"
}

func (j *LocalWriteJournal) recoveryFiles() []string {
	return []string{j.journalFile(), j.compactionFile(), j.legacyCompactionFile()}
}

// AppendEntities 批量合并写入 WAL（同表同主键仅保留最新版本，一次 fsync）。
func (j *LocalWriteJournal) AppendEntities(operation string, entities []IDbEntity) ([]*JournalEntry, error) {
	if len(entities) == 0 {
		return nil, nil
	}
	if j == nil || j.repo == nil {
		return nil, NewValidationException("WAL 未绑定 Repository")
	}
	lockedGeneration := ""
	releaseGeneration := func() {}
	if j.repo.db != nil {
		current, release, err := j.repo.db.lockCurrentDatabaseGeneration()
		if err != nil {
			return nil, err
		}
		lockedGeneration = current
		releaseGeneration = release
	}
	defer releaseGeneration()
	return j.appendEntitiesUnderGenerationLease(operation, entities, lockedGeneration)
}

// appendEntitiesUnderGenerationLease 供 Repository 在 WAL→SQL→清理的同一读租约内调用。
func (j *LocalWriteJournal) appendEntitiesUnderGenerationLease(operation string, entities []IDbEntity, expectedGeneration string) ([]*JournalEntry, error) {
	return j.appendEntitiesInternalUnderGenerationLease(operation, entities, expectedGeneration, true)
}

// appendPreparedEntitiesUnderGenerationLease 供 WriteBuffer 使用；实体 hook 已在首次 Flush 前执行，
// WAL 只捕获同一状态快照，失败重试不得重复调用非幂等 hook。
func (j *LocalWriteJournal) appendPreparedEntitiesUnderGenerationLease(operation string, entities []IDbEntity, expectedGeneration string) ([]*JournalEntry, error) {
	return j.appendEntitiesInternalUnderGenerationLease(operation, entities, expectedGeneration, false)
}

func (j *LocalWriteJournal) appendEntitiesInternalUnderGenerationLease(
	operation string,
	entities []IDbEntity,
	expectedGeneration string,
	runSerializeHook bool,
) ([]*JournalEntry, error) {
	if len(entities) == 0 {
		return nil, nil
	}
	if j == nil || j.repo == nil {
		return nil, NewValidationException("WAL 未绑定 Repository")
	}
	releaseOperation, err := j.beginOperation()
	if err != nil {
		return nil, err
	}
	defer releaseOperation()

	// 用户序列化 hook / MarshalJSON 可能重入数据库或 WAL；不得在 journalMu 下调用。
	preparedEntities := make([]preparedJournalEntity, 0, len(entities))
	cm := GetCrudManagerInstance()
	for _, entity := range entities {
		if entity == nil {
			continue
		}
		tableName := j.repo.getTableName(entity)
		pk := cm.GetPrimaryKeyValue(entity)
		if j.repo.isZeroValue(pk) {
			return nil, NewValidationException(fmt.Sprintf("WAL 要求主键非零: 表=%s", tableName))
		}
		var data []byte
		var serializeErr error
		if runSerializeHook {
			data, serializeErr = SerializeEntity(entity)
		} else {
			data, serializeErr = json.Marshal(entity)
		}
		if serializeErr != nil {
			return nil, serializeErr
		}
		preparedEntities = append(preparedEntities, preparedJournalEntity{
			tableName:      tableName,
			primaryKey:     fmt.Sprintf("%v", pk),
			entityTypeName: EntityTypeName(entity),
			entityJSON:     data,
		})
	}
	// 同一批内同表同主键只保留最后状态，避免为中间状态追加无价值记录。
	latestPrepared := make([]preparedJournalEntity, 0, len(preparedEntities))
	preparedIndex := make(map[journalEntityKey]int, len(preparedEntities))
	for _, entity := range preparedEntities {
		key := journalEntityKey{tableName: entity.tableName, primaryKey: entity.primaryKey}
		if index, ok := preparedIndex[key]; ok {
			latestPrepared[index] = entity
			continue
		}
		preparedIndex[key] = len(latestPrepared)
		latestPrepared = append(latestPrepared, entity)
	}

	j.journalMu.Lock()
	defer j.journalMu.Unlock()
	if err := j.ensureGenerationLocked(); err != nil {
		return nil, err
	}
	if j.repo.db != nil && j.databaseGeneration != expectedGeneration {
		return nil, fmt.Errorf("%w: WAL=%s, Db=%s", ErrDatabaseGenerationChanged, safeValueForLog(j.databaseGeneration), safeValueForLog(expectedGeneration))
	}

	if err := j.ensurePendingCacheLoadedLocked(); err != nil {
		return nil, err
	}
	state := j.stateLocked()
	result := make([]*JournalEntry, 0, len(latestPrepared))
	records := make([]journalLogRecord, 0, len(latestPrepared))
	updates := make([]*JournalEntry, 0, len(latestPrepared))
	now := time.Now()
	batchNonce := ""

	for index, entity := range latestPrepared {
		key := journalEntityKey{tableName: entity.tableName, primaryKey: entity.primaryKey}
		if old, ok := state.pendingCache[key]; ok {
			if old.Operation == operation &&
				old.EntityTypeName == entity.entityTypeName &&
				old.DatabaseGeneration == j.databaseGeneration &&
				bytes.Equal(old.EntityJSON, entity.entityJSON) {
				// 相同恢复载荷已 durable：不刷新时间、不追加、不 fsync。
				result = append(result, cloneJournalEntry(old))
				continue
			}
			if batchNonce == "" {
				batchNonce, err = newJournalBatchNonce()
				if err != nil {
					return nil, fmt.Errorf("生成 WAL 条目版本: %w", err)
				}
			}
			replacement := cloneJournalEntry(old)
			replacement.ID = journalEntryID(now, batchNonce, index)
			replacement.Operation = operation
			replacement.EntityJSON = append([]byte(nil), entity.entityJSON...)
			replacement.EntityTypeName = entity.entityTypeName
			replacement.CreatedAt = now
			replacement.RetryCount = 0
			replacement.LastError = ""
			replacement.DatabaseGeneration = j.databaseGeneration
			replacement.EntitySchemaVersion = j.currentEntitySchemaVersion(entity.tableName)
			result = append(result, replacement)
			updates = append(updates, replacement)
			records = append(records, journalLogRecord{
				FormatVersion:      journalLogFormatVersion,
				Kind:               journalLogKindUpsert,
				DatabaseGeneration: j.databaseGeneration,
				Entry:              replacement,
			})
			continue
		}

		if batchNonce == "" {
			batchNonce, err = newJournalBatchNonce()
			if err != nil {
				return nil, fmt.Errorf("生成 WAL 条目版本: %w", err)
			}
		}
		entry := &JournalEntry{
			ID:                  journalEntryID(now, batchNonce, index),
			Operation:           operation,
			TableName:           entity.tableName,
			PrimaryKey:          entity.primaryKey,
			EntityTypeName:      entity.entityTypeName,
			EntityJSON:          append([]byte(nil), entity.entityJSON...),
			CreatedAt:           now,
			DatabaseGeneration:  j.databaseGeneration,
			EntitySchemaVersion: j.currentEntitySchemaVersion(entity.tableName),
		}
		result = append(result, entry)
		updates = append(updates, entry)
		records = append(records, journalLogRecord{
			FormatVersion:      journalLogFormatVersion,
			Kind:               journalLogKindUpsert,
			DatabaseGeneration: j.databaseGeneration,
			Entry:              entry,
		})
	}

	if len(records) == 0 {
		if err := j.compactIfNeededLocked(); err != nil {
			return result, fmt.Errorf("重试 WAL 自动压缩: %w", err)
		}
		return result, nil
	}
	if err := j.appendLogRecordsLocked(records); err != nil {
		j.invalidatePendingCacheLocked()
		return nil, err
	}
	for _, entry := range updates {
		j.applyUpsertLocked(entry)
	}
	j.rewriteCount.Add(1)
	if err := j.compactIfNeededLocked(); err != nil {
		return result, fmt.Errorf("WAL 已追加但自动压缩失败: %w", err)
	}
	return result, nil
}

func (j *LocalWriteJournal) dedupeEntries(entries []*JournalEntry) map[journalEntityKey]*JournalEntry {
	pending := make(map[journalEntityKey]*JournalEntry, len(entries))
	for _, e := range entries {
		if e == nil {
			continue
		}
		key := journalEntityKey{tableName: e.TableName, primaryKey: e.PrimaryKey}
		if old, ok := pending[key]; !ok || e.CreatedAt.After(old.CreatedAt) {
			pending[key] = e
		}
	}
	return pending
}

// RemoveEntries 写库成功后删除 WAL 条目。
func (j *LocalWriteJournal) RemoveEntries(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	lockedGeneration := ""
	releaseGeneration := func() {}
	if j != nil && j.repo != nil && j.repo.db != nil {
		current, release, err := j.repo.db.lockCurrentDatabaseGeneration()
		if err != nil {
			return err
		}
		lockedGeneration = current
		releaseGeneration = release
	}
	defer releaseGeneration()
	return j.removeEntriesUnderGenerationLease(ids, lockedGeneration)
}

// removeEntriesUnderGenerationLease 供已持有 Db generation 读租约的路径调用。
func (j *LocalWriteJournal) removeEntriesUnderGenerationLease(ids []string, expectedGeneration string) error {
	if len(ids) == 0 {
		return nil
	}
	if j == nil {
		return NewValidationException("WAL 不能为 nil")
	}
	releaseOperation, err := j.beginOperation()
	if err != nil {
		return err
	}
	defer releaseOperation()
	return j.removeEntriesWithLifecycleLease(ids, expectedGeneration)
}

// removeEntriesWithLifecycleLease 要求调用方已登记 WAL lifecycle operation。
// Replay 使用外层 operation，避免 StopStrict 开始后嵌套 beginOperation 被拒绝。
func (j *LocalWriteJournal) removeEntriesWithLifecycleLease(ids []string, expectedGeneration string) error {
	j.journalMu.Lock()
	defer j.journalMu.Unlock()
	if err := j.ensureGenerationLocked(); err != nil {
		return err
	}
	if j.repo != nil && j.repo.db != nil && j.databaseGeneration != expectedGeneration {
		return fmt.Errorf("%w: WAL=%s, Db=%s", ErrDatabaseGenerationChanged, safeValueForLog(j.databaseGeneration), safeValueForLog(expectedGeneration))
	}

	if err := j.ensurePendingCacheLoadedLocked(); err != nil {
		return err
	}
	state := j.stateLocked()
	seen := make(map[string]struct{}, len(ids))
	records := make([]journalLogRecord, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		key, exists := state.keyByEntryID[id]
		if !exists {
			continue
		}
		current := state.pendingCache[key]
		if current == nil || current.ID != id {
			continue
		}
		records = append(records, journalLogRecord{
			FormatVersion:      journalLogFormatVersion,
			Kind:               journalLogKindDelete,
			DatabaseGeneration: j.databaseGeneration,
			EntryID:            id,
		})
	}
	if len(records) == 0 {
		if len(state.pendingCache) == 0 && state.logRecords > 0 {
			if err := removeRecoveryFile(j.journalFile()); err != nil {
				j.invalidatePendingCacheLocked()
				return fmt.Errorf("重试清理空 WAL: %w", err)
			}
			state.logRecords = 0
			state.logBytes = 0
		}
		if err := j.compactIfNeededLocked(); err != nil {
			return fmt.Errorf("重试 WAL 自动压缩: %w", err)
		}
		return nil
	}
	if err := j.appendLogRecordsLocked(records); err != nil {
		j.invalidatePendingCacheLocked()
		return err
	}
	for _, record := range records {
		j.applyDeleteLocked(record.EntryID)
	}
	j.rewriteCount.Add(1)
	if len(state.pendingCache) == 0 {
		if err := removeRecoveryFile(j.journalFile()); err != nil {
			j.invalidatePendingCacheLocked()
			return fmt.Errorf("WAL 删除记录已持久化但空日志清理失败: %w", err)
		}
		state.logRecords = 0
		state.logBytes = 0
		return nil
	}
	if err := j.compactIfNeededLocked(); err != nil {
		return fmt.Errorf("WAL 删除记录已追加但自动压缩失败: %w", err)
	}
	return nil
}

// PendingCount 返回待回放条目数。
func (j *LocalWriteJournal) PendingCount() (int, error) {
	if j == nil {
		return 0, NewValidationException("WAL 不能为 nil")
	}
	releaseOperation, err := j.beginOperation()
	if err != nil {
		return 0, err
	}
	defer releaseOperation()
	j.journalMu.Lock()
	defer j.journalMu.Unlock()
	if err := j.ensureGenerationLocked(); err != nil {
		return 0, err
	}
	if err := j.ensurePendingCacheLoadedLocked(); err != nil {
		return 0, err
	}
	return len(j.stateLocked().pendingCache), nil
}

// ReplayAll 回放所有 pending WAL（启动时或连接恢复后调用）。
func (j *LocalWriteJournal) ReplayAll() (success, failed int) {
	success, failed, err := j.ReplayAllStrict()
	if err != nil && !errors.Is(err, ErrLocalWriteJournalStopped) {
		LogError("WAL 回放未完成: %s", safeErrorForLog(err))
	}
	return success, failed
}

// ReplayAllStrict 回放所有 pending WAL，并传播读取、反序列化、写库及清理错误。
// 数据库写成功但 WAL 清理失败时条目会保留；下次按 at-least-once 语义安全重放。
func (j *LocalWriteJournal) ReplayAllStrict() (success, failed int, replayErr error) {
	if j == nil {
		return 0, 0, NewValidationException("WAL 不能为 nil")
	}
	releaseOperation, err := j.beginOperation()
	if err != nil {
		return 0, 0, err
	}
	defer releaseOperation()
	defer func() { j.setLastReplayError(replayErr) }()

	releaseGeneration := func() {}
	lockedGeneration := ""
	if j.repo != nil && j.repo.db != nil {
		current, release, err := j.repo.db.lockCurrentDatabaseGeneration()
		if err != nil {
			return 0, 0, err
		}
		lockedGeneration = current
		releaseGeneration = release
	}
	defer releaseGeneration()
	return j.replayAllWithLifecycleLeaseStrict(lockedGeneration)
}

// replayAllUnderGenerationLeaseStrict 要求调用方已持有 Db generation 的读锁或写锁。
// 它不会再次获取 generation 锁，供 generation transition 在独占屏障内安全排空 WAL。
func (j *LocalWriteJournal) replayAllUnderGenerationLeaseStrict(expectedGeneration string) (success, failed int, replayErr error) {
	if j == nil {
		return 0, 0, NewValidationException("WAL 不能为 nil")
	}
	releaseOperation, err := j.beginOperation()
	if err != nil {
		return 0, 0, err
	}
	defer releaseOperation()
	defer func() { j.setLastReplayError(replayErr) }()
	return j.replayAllWithLifecycleLeaseStrict(expectedGeneration)
}

// drainUnderGenerationLeaseStrict 严格回放并确认 WAL 已清空。
// 调用方必须持续持有 Db generation 的读锁或写锁，以阻止新写入穿越确认窗口。
func (j *LocalWriteJournal) drainUnderGenerationLeaseStrict(expectedGeneration string) (remaining int, drainErr error) {
	if j == nil {
		return 0, NewValidationException("WAL 不能为 nil")
	}
	releaseOperation, err := j.beginOperation()
	if err != nil {
		return 0, err
	}
	defer releaseOperation()
	defer func() { j.setLastReplayError(drainErr) }()

	_, _, replayErr := j.replayAllWithLifecycleLeaseStrict(expectedGeneration)
	remaining, countErr := j.pendingCountWithLifecycleLeaseStrict(expectedGeneration)
	drainErr = errors.Join(replayErr, countErr)
	if countErr == nil && remaining > 0 {
		drainErr = errors.Join(
			drainErr,
			NewQueryException(fmt.Sprintf("WAL 排空后仍有 %d 条待回放", remaining)),
		)
	}
	return remaining, drainErr
}

// pendingCountWithLifecycleLeaseStrict 要求调用方已登记 lifecycle operation，且持有
// Db generation 租约。generation transition 借此避免嵌套 RWMutex.RLock。
func (j *LocalWriteJournal) pendingCountWithLifecycleLeaseStrict(expectedGeneration string) (int, error) {
	j.journalMu.Lock()
	defer j.journalMu.Unlock()
	if err := j.ensureGenerationLocked(); err != nil {
		return 0, err
	}
	if j.repo != nil && j.repo.db != nil && j.databaseGeneration != expectedGeneration {
		return 0, fmt.Errorf("%w: WAL=%s, Db=%s", ErrDatabaseGenerationChanged, safeValueForLog(j.databaseGeneration), safeValueForLog(expectedGeneration))
	}
	if err := j.ensurePendingCacheLoadedLocked(); err != nil {
		return 0, err
	}
	return len(j.stateLocked().pendingCache), nil
}

// replayAllWithLifecycleLeaseStrict 要求调用方已登记 lifecycle operation，并持有
// Db generation 租约。它承担实际回放，不做可重入的 lifecycle/generation 加锁。
func (j *LocalWriteJournal) replayAllWithLifecycleLeaseStrict(lockedGeneration string) (success, failed int, replayErr error) {
	j.replayMu.Lock()
	defer j.replayMu.Unlock()

	j.journalMu.Lock()
	if err := j.ensureGenerationLocked(); err != nil {
		j.journalMu.Unlock()
		return 0, 0, err
	}
	if j.repo != nil && j.repo.db != nil && j.databaseGeneration != lockedGeneration {
		j.journalMu.Unlock()
		return 0, 0, fmt.Errorf("%w: WAL=%s, Db=%s", ErrDatabaseGenerationChanged, safeValueForLog(j.databaseGeneration), safeValueForLog(lockedGeneration))
	}
	entries, err := j.readAllEntriesLocked()
	j.journalMu.Unlock()
	if err != nil {
		return 0, 0, err
	}
	if len(entries) == 0 {
		return 0, 0, nil
	}
	if j.repo == nil || j.repo.db == nil || j.repo.db.DataSource == nil {
		return 0, len(entries), NewQueryException(fmt.Sprintf("数据库未就绪，WAL 保留 %d 条", len(entries)))
	}

	entries = j.dedupeEntriesToSlice(entries)

	// 按稳定表顺序分组；每表再按 BatchUpsertChunkSize 分块，避免 placeholder
	// 和 max_allowed_packet 上限。失败块保留，其他块仍继续回放。
	grouped := make(map[string][]journalReplayItem)
	tableOrder := make([]string, 0, 4)

	replayErrors := make([]error, 0, 8)
	suppressedErrors := 0
	replayFailures := make([]journalReplayFailure, 0, 8)
	for _, entry := range entries {
		if versionErr := j.validateReplaySchemaVersion(entry); versionErr != nil {
			failed++
			replayFailures = append(replayFailures, journalReplayFailure{entry: entry, cause: versionErr})
			continue
		}
		entity, err := DeserializeEntity(entry.EntityTypeName, entry.EntityJSON)
		if err != nil {
			failed++
			replayFailures = append(replayFailures, journalReplayFailure{entry: entry, cause: err})
			continue
		}
		tableName := j.repo.getTableName(entity)
		if tableName != entry.TableName {
			mismatchErr := NewValidationException(fmt.Sprintf(
				"WAL 实体表与条目不一致: EntryTable=%s, EntityTable=%s",
				safeValueForLog(entry.TableName),
				safeValueForLog(tableName),
			))
			failed++
			replayFailures = append(replayFailures, journalReplayFailure{entry: entry, cause: mismatchErr})
			continue
		}
		if _, exists := grouped[tableName]; !exists {
			tableOrder = append(tableOrder, tableName)
		}
		grouped[tableName] = append(grouped[tableName], journalReplayItem{entry: entry, entity: entity})
	}

	chunkSize := GetCrudPerformanceSettings().Snapshot().BatchUpsertChunkSize
	if chunkSize <= 0 {
		chunkSize = DefaultCrudPerformanceSettings().BatchUpsertChunkSize
	}
	if chunkSize <= 0 {
		chunkSize = 200
	}
	succeededIDs := make([]string, 0, len(entries))
	for _, tableName := range tableOrder {
		items := grouped[tableName]
		for start := 0; start < len(items); start += chunkSize {
			end := start + chunkSize
			if end > len(items) {
				end = len(items)
			}
			chunk := items[start:end]
			entities := make([]IDbEntity, len(chunk))
			for index, item := range chunk {
				entities[index] = item.entity
			}
			if err := j.repo.saveBatchUpsertOncePreparedUnderGenerationLease(entities, lockedGeneration); err != nil {
				failed += len(chunk)
				for _, item := range chunk {
					replayFailures = append(replayFailures, journalReplayFailure{entry: item.entry, cause: err})
				}
				continue
			}
			for _, item := range chunk {
				succeededIDs = append(succeededIDs, item.entry.ID)
				success++
			}
		}
	}

	if len(succeededIDs) > 0 || len(replayFailures) > 0 {
		nonTerminalErrors, failurePersistErr := j.persistReplayResultsWithLifecycleLease(succeededIDs, replayFailures, lockedGeneration)
		for _, nonTerminalErr := range nonTerminalErrors {
			replayErrors = appendBoundedRecoveryError(replayErrors, nonTerminalErr, &suppressedErrors)
		}
		if failurePersistErr != nil {
			replayErrors = appendBoundedRecoveryError(replayErrors, failurePersistErr, &suppressedErrors)
		}
	}
	if success > 0 {
		LogInfo("WAL 回放完成: 成功=%d, 失败=%d", success, failed)
	}
	if suppressedErrors > 0 {
		replayErrors = append(replayErrors, fmt.Errorf("另有 %d 个 WAL 回放错误已省略", suppressedErrors))
	}
	return success, failed, errors.Join(replayErrors...)
}

func (j *LocalWriteJournal) validateReplaySchemaVersion(entry *JournalEntry) error {
	if entry == nil {
		return NewValidationException("WAL 条目不能为空")
	}
	current := j.currentEntitySchemaVersion(entry.TableName)
	if current == entry.EntitySchemaVersion {
		return nil
	}
	return NewValidationException(fmt.Sprintf(
		"WAL Entity 表结构版本不一致: Table=%s, WAL=%d, Current=%d",
		safeValueForLog(entry.TableName),
		entry.EntitySchemaVersion,
		current,
	))
}

func (j *LocalWriteJournal) currentEntitySchemaVersion(tableName string) int64 {
	if j == nil || j.repo == nil || j.repo.db == nil {
		return 0
	}
	return j.repo.db.EntitySchemaVersion(tableName)
}

// persistReplayResultsWithLifecycleLease 用一次 fsync 清理成功项并持久化失败次数；
// 达到上限时先写死信，再从 WAL 删除。
// 死信写入失败时保留原 WAL，保证恢复证据不会因清理顺序丢失。
func (j *LocalWriteJournal) persistReplayResultsWithLifecycleLease(
	succeededIDs []string,
	failures []journalReplayFailure,
	expectedGeneration string,
) ([]error, error) {
	j.journalMu.Lock()
	defer j.journalMu.Unlock()
	if err := j.ensureGenerationLocked(); err != nil {
		return nil, err
	}
	if j.repo != nil && j.repo.db != nil && j.databaseGeneration != expectedGeneration {
		return nil, fmt.Errorf("%w: WAL=%s, Db=%s", ErrDatabaseGenerationChanged, safeValueForLog(j.databaseGeneration), safeValueForLog(expectedGeneration))
	}
	if err := j.ensurePendingCacheLoadedLocked(); err != nil {
		return nil, err
	}

	state := j.stateLocked()
	records := make([]journalLogRecord, 0, len(succeededIDs)+len(failures))
	nonTerminalErrors := make([]error, 0, len(failures))
	terminalLogs := make([]string, 0, len(failures))
	seen := make(map[string]struct{}, len(succeededIDs)+len(failures))
	for _, id := range succeededIDs {
		key, exists := state.keyByEntryID[id]
		if !exists {
			continue
		}
		current := state.pendingCache[key]
		if current == nil || current.ID != id {
			continue
		}
		seen[id] = struct{}{}
		records = append(records, journalLogRecord{
			FormatVersion:      journalLogFormatVersion,
			Kind:               journalLogKindDelete,
			DatabaseGeneration: j.databaseGeneration,
			EntryID:            id,
		})
	}
	for _, failure := range failures {
		if failure.entry == nil || failure.entry.ID == "" {
			continue
		}
		if _, duplicate := seen[failure.entry.ID]; duplicate {
			continue
		}
		seen[failure.entry.ID] = struct{}{}
		key, exists := state.keyByEntryID[failure.entry.ID]
		if !exists {
			continue
		}
		current := state.pendingCache[key]
		if current == nil || current.ID != failure.entry.ID {
			continue
		}
		updated := cloneJournalEntry(current)
		updated.RetryCount++
		updated.LastError = safeErrorSummary(failure.cause)

		if updated.RetryCount >= j.recoveryMaxAttempts {
			path, err := persistRecoveryDeadLetter(j.dir, recoveryDeadLetter{
				Component:           "wal",
				EntryID:             updated.ID,
				TableName:           updated.TableName,
				PrimaryKey:          updated.PrimaryKey,
				EntityTypeName:      updated.EntityTypeName,
				Operation:           updated.Operation,
				RetryCount:          updated.RetryCount,
				LastError:           updated.LastError,
				EntitySchemaVersion: updated.EntitySchemaVersion,
				CreatedAt:           updated.CreatedAt,
				TerminalAt:          time.Now(),
				Payload:             updated,
			})
			if err != nil {
				nonTerminalErrors = append(nonTerminalErrors, fmt.Errorf("WAL 死信持久化失败: ID=%s: %w", safeValueForLog(updated.ID), err))
				records = append(records, journalLogRecord{
					FormatVersion:      journalLogFormatVersion,
					Kind:               journalLogKindUpsert,
					DatabaseGeneration: j.databaseGeneration,
					Entry:              updated,
				})
				continue
			}
			records = append(records, journalLogRecord{
				FormatVersion:      journalLogFormatVersion,
				Kind:               journalLogKindDelete,
				DatabaseGeneration: j.databaseGeneration,
				EntryID:            updated.ID,
			})
			terminalLogs = append(terminalLogs, filepath.Base(path))
			continue
		}

		records = append(records, journalLogRecord{
			FormatVersion:      journalLogFormatVersion,
			Kind:               journalLogKindUpsert,
			DatabaseGeneration: j.databaseGeneration,
			Entry:              updated,
		})
		nonTerminalErrors = append(nonTerminalErrors, NewQueryExceptionWithCause(
			failure.cause,
			fmt.Sprintf(
				"WAL 回放失败: ID=%s, Attempt=%d/%d",
				safeValueForLog(updated.ID),
				updated.RetryCount,
				j.recoveryMaxAttempts,
			),
		))
	}
	if len(records) == 0 {
		return nonTerminalErrors, nil
	}
	if err := j.appendLogRecordsLocked(records); err != nil {
		j.invalidatePendingCacheLocked()
		return nonTerminalErrors, err
	}
	for _, record := range records {
		if record.Kind == journalLogKindDelete {
			j.applyDeleteLocked(record.EntryID)
		} else {
			j.applyUpsertLocked(record.Entry)
		}
	}
	j.rewriteCount.Add(1)
	if len(state.pendingCache) == 0 {
		if err := removeRecoveryFile(j.journalFile()); err != nil {
			j.invalidatePendingCacheLocked()
			return nonTerminalErrors, fmt.Errorf("WAL 结果已持久化但空日志清理失败: %w", err)
		}
		state.logRecords = 0
		state.logBytes = 0
	}
	for _, path := range terminalLogs {
		LogError("WAL 条目重试达到上限，已转入死信，需人工处理: DeadLetterRef=%s", path)
	}
	return nonTerminalErrors, nil
}

func (j *LocalWriteJournal) replayLoop() {
	defer j.doneOnce.Do(func() { close(j.doneCh) })
	ticker := time.NewTicker(j.retryInterval())
	defer ticker.Stop()

	// 启动立即回放一次；严格错误保留在 LastReplayError，并输出无敏感值摘要。
	if _, _, err := j.ReplayAllStrict(); err != nil && !errors.Is(err, ErrLocalWriteJournalStopped) {
		LogWarn("启动 WAL 回放未完成: %s", safeErrorForLog(err))
	}

	for {
		select {
		case <-j.stopCh:
			return
		case <-j.intervalChanged:
			ticker.Reset(j.retryInterval())
		case <-ticker.C:
			if _, _, err := j.ReplayAllStrict(); err != nil && !errors.Is(err, ErrLocalWriteJournalStopped) {
				LogWarn("后台 WAL 回放未完成: %s", safeErrorForLog(err))
			}
		}
	}
}

func (j *LocalWriteJournal) retryInterval() time.Duration {
	j.lifecycleMu.Lock()
	defer j.lifecycleMu.Unlock()
	if j.interval <= 0 {
		return 5 * time.Second
	}
	return j.interval
}

func (j *LocalWriteJournal) ensurePendingCacheLoadedLocked() error {
	if j.stateLocked().pendingCacheLoaded {
		return nil
	}
	_, err := j.readAllEntriesLocked()
	return err
}

func (j *LocalWriteJournal) readAllEntriesLocked() ([]*JournalEntry, error) {
	state := j.stateLocked()
	if state.pendingCacheLoaded {
		return j.pendingEntriesSnapshotLocked(), nil
	}
	path := j.journalFile()
	if err := j.cleanupCompactionFileLocked(); err != nil {
		return nil, fmt.Errorf("清理未完成 WAL 压缩文件: %w", err)
	}
	if err := ensurePrivateRecoveryFileIfExists(path); err != nil {
		return nil, fmt.Errorf("收紧 WAL 权限: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			j.replacePendingCacheLocked(nil)
			return j.pendingEntriesSnapshotLocked(), nil
		}
		return nil, err
	}
	reader := bufio.NewReaderSize(f, 64<<10)
	loaded := &localWriteJournalState{
		pendingCache:    make(map[journalEntityKey]*JournalEntry),
		keyByEntryID:    make(map[string]journalEntityKey),
		appendedBytes:   state.appendedBytes,
		compactionCount: state.compactionCount,
		tornTailCount:   state.tornTailCount,
	}
	var completeBytes int64
	legacyFormat := false
	versionedFormat := false
	seenUpsertIDs := make(map[string]struct{})
	tornTail := false
	var corruptErr error
	corruptComponent := "wal-corrupt"
	for {
		line, terminated, eof, consumed, lineErr := readBoundedJournalLine(reader)
		if lineErr != nil {
			if errors.Is(lineErr, errJournalRecordTooLarge) && eof && !terminated {
				tornTail = true
				break
			}
			if errors.Is(lineErr, errJournalRecordTooLarge) {
				corruptErr = lineErr
				break
			}
			closeErr := f.Close()
			return nil, errors.Join(fmt.Errorf("读取 WAL: %w", lineErr), closeErr)
		}
		if !terminated {
			if eof && consumed > 0 {
				tornTail = true
			}
			break
		}
		completeBytes += consumed
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		legacy, record, entry, parseErr := decodeJournalLogLine(line)
		if parseErr != nil {
			corruptErr = parseErr
			break
		}
		loaded.logRecords++
		if legacy {
			if versionedFormat {
				corruptErr = errors.New("WAL 同时包含旧版与版本化记录")
				corruptComponent = "wal-mixed-format"
				break
			}
			legacyFormat = true
			if mismatch := j.journalGenerationMismatch(entry.DatabaseGeneration); mismatch != nil {
				corruptErr = mismatch
				corruptComponent = "wal-entry-mismatch"
				break
			}
			if _, duplicate := seenUpsertIDs[entry.ID]; duplicate {
				corruptErr = errors.New("WAL 包含重复条目标识")
				corruptComponent = "wal-duplicate-id"
				break
			}
			seenUpsertIDs[entry.ID] = struct{}{}
			entry.LastError = sanitizePersistedErrorSummary(entry.LastError)
			applyLegacyUpsertToState(loaded, entry)
			continue
		}
		if legacyFormat {
			corruptErr = errors.New("WAL 同时包含旧版与版本化记录")
			corruptComponent = "wal-mixed-format"
			break
		}
		versionedFormat = true
		if mismatch := j.journalGenerationMismatch(record.DatabaseGeneration); mismatch != nil {
			corruptErr = mismatch
			corruptComponent = "wal-entry-mismatch"
			break
		}
		switch record.Kind {
		case journalLogKindUpsert:
			if _, duplicate := seenUpsertIDs[record.Entry.ID]; duplicate {
				corruptErr = errors.New("WAL 包含重复条目标识")
				corruptComponent = "wal-duplicate-id"
				break
			}
			seenUpsertIDs[record.Entry.ID] = struct{}{}
			record.Entry.LastError = sanitizePersistedErrorSummary(record.Entry.LastError)
			applyUpsertToState(loaded, record.Entry)
		case journalLogKindDelete:
			applyDeleteToState(loaded, record.EntryID)
		}
		if corruptErr != nil {
			break
		}
	}
	closeErr := f.Close()
	if corruptErr != nil {
		j.invalidatePendingCacheLocked()
		return nil, j.quarantineJournalLocked(path, corruptComponent, errors.Join(corruptErr, closeErr))
	}
	if closeErr != nil {
		return nil, fmt.Errorf("关闭 WAL: %w", closeErr)
	}
	if tornTail {
		if err := truncateAndSyncRecoveryFile(path, completeBytes); err != nil {
			j.invalidatePendingCacheLocked()
			return nil, fmt.Errorf("截断 WAL 尾部撕裂记录: %w", err)
		}
		loaded.tornTailCount++
	}
	loaded.pendingCacheLoaded = true
	loaded.logBytes = completeBytes
	j.state = loaded
	if legacyFormat {
		// v1.0.x 直接 JournalEntry NDJSON 在首次读取时原子迁移；原文件在
		// Rename+目录 fsync 成功前始终保留。
		if err := j.rewriteEntriesLocked(j.pendingEntriesSnapshotLocked()); err != nil {
			j.invalidatePendingCacheLocked()
			return nil, fmt.Errorf("迁移旧版 WAL: %w", err)
		}
	} else if len(loaded.pendingCache) == 0 && loaded.logRecords > 0 {
		if err := removeRecoveryFile(path); err != nil {
			j.invalidatePendingCacheLocked()
			return nil, fmt.Errorf("清理空 WAL: %w", err)
		}
		loaded.logRecords = 0
		loaded.logBytes = 0
	}
	return j.pendingEntriesSnapshotLocked(), nil
}

func (j *LocalWriteJournal) rewriteEntriesLocked(entries []*JournalEntry) (resultErr error) {
	path := j.journalFile()
	if err := j.ensurePathOwnershipLocked(); err != nil {
		return err
	}
	if err := ensurePrivateRecoveryDirectory(j.dir); err != nil {
		return fmt.Errorf("创建 WAL 目录失败: %w", err)
	}

	if len(entries) == 0 {
		if err := removeRecoveryFile(path); err != nil {
			j.invalidatePendingCacheLocked()
			return err
		}
		j.replacePendingCacheLocked(nil)
		state := j.stateLocked()
		state.logRecords = 0
		state.logBytes = 0
		state.compactionCount++
		j.rewriteCount.Add(1)
		return nil
	}

	if err := ensurePrivateRecoveryFileIfExists(path); err != nil {
		return err
	}
	tmpPath := j.compactionFile()
	if err := j.cleanupCompactionFileLocked(); err != nil {
		return err
	}
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, recoveryFileMode)
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := removeRecoveryFile(tmpPath); cleanupErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("清理 WAL 压缩临时文件: %w", cleanupErr))
		}
	}()
	if err := f.Chmod(recoveryFileMode); err != nil {
		return closeFileWithError(f, err)
	}
	if err := hardenRecoveryFilePermissions(tmpPath); err != nil {
		return closeFileWithError(f, err)
	}
	entries = j.dedupeEntriesToSlice(entries)
	var written int64
	for _, entry := range entries {
		record := journalLogRecord{
			FormatVersion:      journalLogFormatVersion,
			Kind:               journalLogKindUpsert,
			DatabaseGeneration: entry.DatabaseGeneration,
			Entry:              entry,
		}
		line, err := encodeJournalLogRecord(record)
		if err != nil {
			return closeFileWithError(f, err)
		}
		if err := writeAll(f, line); err != nil {
			return closeFileWithError(f, err)
		}
		written += int64(len(line))
	}
	if err := f.Sync(); err != nil {
		return closeFileWithError(f, err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if err := hardenRecoveryFilePermissions(path); err != nil {
		j.invalidatePendingCacheLocked()
		return err
	}
	if err := syncRecoveryDirectory(j.dir); err != nil {
		j.invalidatePendingCacheLocked()
		return err
	}
	j.replacePendingCacheLocked(entries)
	state := j.stateLocked()
	state.logRecords = uint64(len(entries))
	state.logBytes = written
	state.compactionCount++
	j.rewriteCount.Add(1)
	return nil
}

func (j *LocalWriteJournal) appendLogRecordsLocked(records []journalLogRecord) error {
	if len(records) == 0 {
		return nil
	}
	if err := j.ensurePathOwnershipLocked(); err != nil {
		return err
	}
	if err := ensurePrivateRecoveryDirectory(j.dir); err != nil {
		return fmt.Errorf("创建 WAL 目录失败: %w", err)
	}

	var payload bytes.Buffer
	for _, record := range records {
		line, err := encodeJournalLogRecord(record)
		if err != nil {
			return err
		}
		if _, err := payload.Write(line); err != nil {
			return err
		}
	}

	path := j.journalFile()
	state := j.stateLocked()
	info, statErr := os.Lstat(path)
	existed := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	if existed {
		if err := ensurePrivateRecoveryFileIfExists(path); err != nil {
			return err
		}
		if info.Size() != state.logBytes {
			j.invalidatePendingCacheLocked()
			j.generationErr = fmt.Errorf("%w: WAL 文件长度与已加载状态不一致", ErrDatabaseGenerationBlocked)
			return j.generationErr
		}
	} else if state.logBytes != 0 || len(state.pendingCache) != 0 {
		j.invalidatePendingCacheLocked()
		j.generationErr = fmt.Errorf("%w: WAL 文件缺失但内存仍有待写状态", ErrDatabaseGenerationBlocked)
		return j.generationErr
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, recoveryFileMode)
	if err != nil {
		return err
	}
	cleanupFailedAppend := func(cause error) error {
		resultErr := closeFileWithError(f, cause)
		if !existed {
			resultErr = errors.Join(resultErr, removeRecoveryFile(path))
		}
		return resultErr
	}
	if err := f.Chmod(recoveryFileMode); err != nil {
		return cleanupFailedAppend(err)
	}
	if err := hardenRecoveryFilePermissions(path); err != nil {
		return cleanupFailedAppend(err)
	}
	if err := writeAll(f, payload.Bytes()); err != nil {
		return cleanupFailedAppend(err)
	}
	if err := f.Sync(); err != nil {
		return cleanupFailedAppend(err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	if !existed {
		if err := syncRecoveryDirectory(j.dir); err != nil {
			return err
		}
	}
	state.logRecords += uint64(len(records))
	state.logBytes += int64(payload.Len())
	state.appendedBytes += uint64(payload.Len())
	return nil
}

func (j *LocalWriteJournal) compactIfNeededLocked() error {
	state := j.stateLocked()
	if !state.pendingCacheLoaded || state.logRecords <= uint64(len(state.pendingCache)) {
		return nil
	}
	obsolete := state.logRecords - uint64(len(state.pendingCache))
	hardByteLimitReached := state.logBytes >= journalCompactionHardBytes
	if obsolete < journalCompactionMinObsolete && !hardByteLimitReached {
		return nil
	}
	records := state.logRecords
	requiredByRatio := records / 100 * journalCompactionObsoletePercent
	if remainder := records % 100; remainder != 0 {
		requiredByRatio += (remainder*journalCompactionObsoletePercent + 99) / 100
	}
	if obsolete < requiredByRatio && !hardByteLimitReached {
		return nil
	}
	return j.rewriteEntriesLocked(j.pendingEntriesSnapshotLocked())
}

func encodeJournalLogRecord(record journalLogRecord) ([]byte, error) {
	if err := validateJournalLogRecord(record); err != nil {
		return nil, err
	}
	line, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	if len(line)+1 > journalMaxRecordBytes {
		return nil, fmt.Errorf("WAL 记录超过 %d 字节上限", journalMaxRecordBytes)
	}
	return append(line, '\n'), nil
}

func validateJournalLogRecord(record journalLogRecord) error {
	if record.FormatVersion != journalLogFormatVersion {
		return fmt.Errorf("不支持的 WAL 格式版本: %d", record.FormatVersion)
	}
	switch record.Kind {
	case journalLogKindUpsert:
		if record.Entry == nil || record.EntryID != "" {
			return errors.New("WAL upsert 记录结构无效")
		}
		if record.Entry.DatabaseGeneration != record.DatabaseGeneration {
			return errors.New("WAL upsert 记录代次不一致")
		}
		return validateJournalEntry(record.Entry)
	case journalLogKindDelete:
		if record.Entry != nil || record.EntryID == "" {
			return errors.New("WAL delete 记录结构无效")
		}
		return nil
	default:
		return errors.New("WAL 记录 kind 无效")
	}
}

func validateJournalEntry(entry *JournalEntry) error {
	if entry == nil || entry.ID == "" || entry.Operation == "" || entry.TableName == "" || entry.PrimaryKey == "" || entry.EntityTypeName == "" {
		return errors.New("WAL 条目缺少必需字段")
	}
	if len(entry.EntityJSON) == 0 {
		return errors.New("WAL 条目缺少实体快照")
	}
	return nil
}

func decodeJournalLogLine(line []byte) (legacy bool, record journalLogRecord, entry *JournalEntry, err error) {
	var marker struct {
		FormatVersion *int `json:"_db233WalVersion"`
	}
	if err := json.Unmarshal(line, &marker); err != nil {
		return false, record, nil, fmt.Errorf("解析 WAL JSON: %w", err)
	}
	if marker.FormatVersion == nil {
		var legacyEntry JournalEntry
		if err := json.Unmarshal(line, &legacyEntry); err != nil {
			return false, record, nil, fmt.Errorf("解析旧版 WAL: %w", err)
		}
		if err := validateJournalEntry(&legacyEntry); err != nil {
			return false, record, nil, err
		}
		return true, record, &legacyEntry, nil
	}
	if err := json.Unmarshal(line, &record); err != nil {
		return false, record, nil, fmt.Errorf("解析 WAL 记录: %w", err)
	}
	if err := validateJournalLogRecord(record); err != nil {
		return false, record, nil, err
	}
	return false, record, nil, nil
}

func newJournalBatchNonce() (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(nonce[:]), nil
}

func journalEntryID(createdAt time.Time, nonce string, index int) string {
	return fmt.Sprintf("%d_%s_%d", createdAt.UnixNano(), nonce, index)
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func closeFileWithError(file *os.File, cause error) error {
	if file == nil {
		return cause
	}
	return errors.Join(cause, file.Close())
}

var errJournalRecordTooLarge = errors.New("WAL 单条记录超过安全上限")

func readBoundedJournalLine(reader *bufio.Reader) (line []byte, terminated, eof bool, consumed int64, err error) {
	var buffer bytes.Buffer
	overflow := false
	for {
		fragment, readErr := reader.ReadSlice('\n')
		consumed += int64(len(fragment))
		if !overflow {
			if buffer.Len()+len(fragment) > journalMaxRecordBytes {
				overflow = true
			} else {
				_, _ = buffer.Write(fragment)
			}
		}
		switch {
		case readErr == nil:
			if overflow {
				return nil, true, false, consumed, errJournalRecordTooLarge
			}
			return buffer.Bytes(), true, false, consumed, nil
		case errors.Is(readErr, bufio.ErrBufferFull):
			continue
		case errors.Is(readErr, io.EOF):
			if overflow {
				return nil, false, true, consumed, errJournalRecordTooLarge
			}
			return buffer.Bytes(), false, true, consumed, nil
		default:
			return nil, false, false, consumed, readErr
		}
	}
}

func truncateAndSyncRecoveryFile(path string, size int64) error {
	if err := ensurePrivateRecoveryFileIfExists(path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY, recoveryFileMode)
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
		return closeFileWithError(f, err)
	}
	if err := f.Sync(); err != nil {
		return closeFileWithError(f, err)
	}
	return f.Close()
}

func (j *LocalWriteJournal) cleanupCompactionFileLocked() error {
	tmpPaths := []string{j.compactionFile(), j.legacyCompactionFile()}
	present := make([]string, 0, len(tmpPaths))
	for _, tmpPath := range tmpPaths {
		if err := ensurePrivateRecoveryFileIfExists(tmpPath); err != nil {
			return err
		}
		if _, err := os.Lstat(tmpPath); err == nil {
			present = append(present, tmpPath)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if len(present) == 0 {
		return nil
	}
	mainPath := j.journalFile()
	if _, err := os.Lstat(mainPath); err == nil {
		if err := ensurePrivateRecoveryFileIfExists(mainPath); err != nil {
			return err
		}
		// Rename 尚未发生：旧主文件仍是已提交快照，临时文件可安全丢弃。
		var cleanupErrors []error
		for _, tmpPath := range present {
			cleanupErrors = append(cleanupErrors, removeRecoveryFile(tmpPath))
		}
		return errors.Join(cleanupErrors...)
	} else if !os.IsNotExist(err) {
		return err
	}
	if len(present) != 1 {
		var quarantineErrors []error
		for _, tmpPath := range present {
			quarantineErrors = append(quarantineErrors, quarantineRecoveryFile(j.dir, tmpPath, "wal-compaction-conflict"))
		}
		j.invalidatePendingCacheLocked()
		j.generationErr = NewQueryExceptionWithCause(
			errors.Join(ErrDatabaseGenerationBlocked, errors.Join(quarantineErrors...)),
			"WAL 压缩恢复候选冲突",
		)
		return j.generationErr
	}
	tmpPath := present[0]

	// 非 Unix 平台 Rename 的崩溃窗口可能只留下已 fsync 的临时快照。
	// 仅完整、同代次、单一格式的快照可被提升；任何不确定性均隔离并阻断。
	allowLegacy := filepath.Clean(tmpPath) == filepath.Clean(j.legacyCompactionFile())
	if err := j.validateCompactionSnapshotLocked(tmpPath, allowLegacy); err != nil {
		j.invalidatePendingCacheLocked()
		return j.quarantineJournalLocked(tmpPath, "wal-compaction-incomplete", err)
	}
	if err := os.Rename(tmpPath, mainPath); err != nil {
		return fmt.Errorf("恢复 WAL 压缩快照: %w", err)
	}
	if err := hardenRecoveryFilePermissions(mainPath); err != nil {
		return err
	}
	return syncRecoveryDirectory(j.dir)
}

func (j *LocalWriteJournal) validateCompactionSnapshotLocked(path string, allowLegacy bool) (resultErr error) {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, f.Close()) }()
	reader := bufio.NewReaderSize(f, 64<<10)
	recordCount := 0
	seenUpsertIDs := make(map[string]struct{})
	for {
		line, terminated, eof, consumed, lineErr := readBoundedJournalLine(reader)
		if lineErr != nil {
			return lineErr
		}
		if !terminated {
			if consumed != 0 || !eof || recordCount == 0 {
				return errors.New("WAL 压缩快照未完整提交")
			}
			return nil
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		legacy, record, entry, err := decodeJournalLogLine(line)
		if err != nil {
			return err
		}
		if legacy {
			if !allowLegacy {
				return errors.New("WAL 压缩快照包含旧版记录")
			}
			if err := j.journalGenerationMismatch(entry.DatabaseGeneration); err != nil {
				return err
			}
			if _, duplicate := seenUpsertIDs[entry.ID]; duplicate {
				return errors.New("WAL 压缩快照包含重复条目标识")
			}
			seenUpsertIDs[entry.ID] = struct{}{}
			recordCount++
			continue
		}
		if allowLegacy || record.Kind != journalLogKindUpsert {
			return errors.New("WAL 压缩快照包含非 upsert 记录")
		}
		if err := j.journalGenerationMismatch(record.DatabaseGeneration); err != nil {
			return err
		}
		if _, duplicate := seenUpsertIDs[record.Entry.ID]; duplicate {
			return errors.New("WAL 压缩快照包含重复条目标识")
		}
		seenUpsertIDs[record.Entry.ID] = struct{}{}
		recordCount++
	}
}

func (j *LocalWriteJournal) journalGenerationMismatch(actual string) error {
	if j.databaseGeneration == "" || actual == j.databaseGeneration {
		return nil
	}
	return fmt.Errorf(
		"WAL 记录 generation=%s, 当前=%s",
		safeValueForLog(actual),
		safeValueForLog(j.databaseGeneration),
	)
}

func (j *LocalWriteJournal) quarantineJournalLocked(path, component string, cause error) error {
	quarantineErr := quarantineRecoveryFile(j.dir, path, component)
	message := "WAL 损坏，已隔离"
	if quarantineErr != nil {
		message = "WAL 损坏且隔离失败"
	}
	// Error 文本保持脱敏，同时让 errors.Is/errors.As 穿透到原始读取、关闭和
	// 隔离错误，避免严格调用方只能得到一段不可诊断字符串。
	j.generationErr = NewQueryExceptionWithCause(
		errors.Join(ErrDatabaseGenerationBlocked, cause, quarantineErr),
		message,
	)
	return j.generationErr
}

func (j *LocalWriteJournal) dedupeEntriesToSlice(entries []*JournalEntry) []*JournalEntry {
	pending := j.dedupeEntries(entries)
	result := make([]*JournalEntry, 0, len(pending))
	for _, entry := range pending {
		result = append(result, entry)
	}
	sortJournalEntrySlice(result)
	return result
}

func (j *LocalWriteJournal) invalidatePendingCacheLocked() {
	state := j.stateLocked()
	state.pendingCache = nil
	state.keyByEntryID = nil
	state.pendingCacheLoaded = false
	state.logRecords = 0
	state.logBytes = 0
}

func (j *LocalWriteJournal) replacePendingCacheLocked(entries []*JournalEntry) {
	state := j.stateLocked()
	state.pendingCache = make(map[journalEntityKey]*JournalEntry, len(entries))
	state.keyByEntryID = make(map[string]journalEntityKey, len(entries))
	for _, entry := range j.dedupeEntries(entries) {
		applyUpsertToState(state, entry)
	}
	state.pendingCacheLoaded = true
}

func (j *LocalWriteJournal) pendingEntriesSnapshotLocked() []*JournalEntry {
	state := j.stateLocked()
	if state.pendingCache == nil {
		return nil
	}
	result := make([]*JournalEntry, 0, len(state.pendingCache))
	for _, entry := range state.pendingCache {
		if entry != nil {
			result = append(result, entry)
		}
	}
	sortJournalEntrySlice(result)
	for index, entry := range result {
		result[index] = cloneJournalEntry(entry)
	}
	return result
}

func sortJournalEntrySlice(entries []*JournalEntry) {
	sort.Slice(entries, func(left, right int) bool {
		if entries[left].CreatedAt.Equal(entries[right].CreatedAt) {
			return entries[left].ID < entries[right].ID
		}
		return entries[left].CreatedAt.Before(entries[right].CreatedAt)
	})
}

func cloneJournalEntry(source *JournalEntry) *JournalEntry {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.EntityJSON = append([]byte(nil), source.EntityJSON...)
	cloned.Params = make([]any, len(source.Params))
	for index, value := range source.Params {
		cloned.Params[index] = cloneRecoveryValue(value)
	}
	return &cloned
}

func (j *LocalWriteJournal) stateLocked() *localWriteJournalState {
	if j.state == nil {
		j.state = &localWriteJournalState{}
	}
	return j.state
}

func applyLegacyUpsertToState(state *localWriteJournalState, entry *JournalEntry) {
	key := journalEntityKey{tableName: entry.TableName, primaryKey: entry.PrimaryKey}
	if current := state.pendingCache[key]; current != nil && !entry.CreatedAt.After(current.CreatedAt) {
		return
	}
	applyUpsertToState(state, entry)
}

func applyUpsertToState(state *localWriteJournalState, entry *JournalEntry) {
	if state.pendingCache == nil {
		state.pendingCache = make(map[journalEntityKey]*JournalEntry)
	}
	if state.keyByEntryID == nil {
		state.keyByEntryID = make(map[string]journalEntityKey)
	}
	key := journalEntityKey{tableName: entry.TableName, primaryKey: entry.PrimaryKey}
	if current := state.pendingCache[key]; current != nil {
		delete(state.keyByEntryID, current.ID)
	}
	cloned := cloneJournalEntry(entry)
	state.pendingCache[key] = cloned
	state.keyByEntryID[cloned.ID] = key
}

func applyDeleteToState(state *localWriteJournalState, entryID string) {
	key, exists := state.keyByEntryID[entryID]
	if !exists {
		return
	}
	current := state.pendingCache[key]
	if current == nil || current.ID != entryID {
		delete(state.keyByEntryID, entryID)
		return
	}
	delete(state.pendingCache, key)
	delete(state.keyByEntryID, entryID)
}

func (j *LocalWriteJournal) applyUpsertLocked(entry *JournalEntry) {
	applyUpsertToState(j.stateLocked(), entry)
}

func (j *LocalWriteJournal) applyDeleteLocked(entryID string) {
	applyDeleteToState(j.stateLocked(), entryID)
}

func (j *LocalWriteJournal) beginOperation() (func(), error) {
	j.lifecycleMu.Lock()
	defer j.lifecycleMu.Unlock()
	if j.stopped {
		return func() {}, ErrLocalWriteJournalStopped
	}
	j.operationWG.Add(1)
	return j.operationWG.Done, nil
}

func (j *LocalWriteJournal) setLastReplayError(err error) {
	j.lifecycleMu.Lock()
	j.lastReplayErr = err
	j.lifecycleMu.Unlock()
}

// LastReplayError 返回最近一次尚未被成功回放覆盖的错误。
func (j *LocalWriteJournal) LastReplayError() error {
	if j == nil {
		return nil
	}
	j.lifecycleMu.Lock()
	defer j.lifecycleMu.Unlock()
	return j.lastReplayErr
}

func (j *LocalWriteJournal) ensurePathOwnershipLocked() error {
	if j.pathLock != nil {
		expected := filepath.Clean(filepath.Join(j.dir, "pending.ndjson.lock"))
		if filepath.Clean(j.pathLockPath) != expected {
			return fmt.Errorf("%w: WAL 目录在持有路径锁期间发生变化", ErrLocalWriteJournalPathInUse)
		}
		return nil
	}
	if err := ensurePrivateRecoveryDirectory(j.dir); err != nil {
		return err
	}
	lockPath := filepath.Join(j.dir, "pending.ndjson.lock")
	if err := ensurePrivateRecoveryFileIfExists(lockPath); err != nil {
		return err
	}
	pathLock := flock.New(lockPath, flock.SetPermissions(recoveryFileMode))
	locked, err := pathLock.TryLock()
	if err != nil {
		return fmt.Errorf("获取 WAL advisory lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("%w: %s", ErrLocalWriteJournalPathInUse, safeValueForLog(lockPath))
	}
	cleanup := func(cause error) error {
		unlockErr := pathLock.Unlock()
		if unlockErr != nil {
			unlockErr = fmt.Errorf("释放 WAL advisory lock: %w", unlockErr)
		}
		return errors.Join(cause, unlockErr)
	}
	if err := ensurePrivateRecoveryFileIfExists(lockPath); err != nil {
		return cleanup(err)
	}
	if err := syncRecoveryDirectory(j.dir); err != nil {
		return cleanup(err)
	}
	j.pathLock = pathLock
	j.pathLockPath = lockPath
	return nil
}

func (j *LocalWriteJournal) releasePathOwnershipLocked() error {
	if j.pathLock == nil {
		return nil
	}
	unlockErr := j.pathLock.Unlock()
	if unlockErr != nil {
		// 解锁失败时保留引用并 fail closed；进程退出仍会由操作系统释放锁。
		return fmt.Errorf("释放 WAL advisory lock: %w", unlockErr)
	}
	j.pathLock = nil
	j.pathLockPath = ""
	// 锁文件是稳定的锁定对象，不能在 Unlock 后删除：另一进程可能已获取旧 inode，
	// 删除并重建会允许第三个进程锁住新 inode。残留文件本身不代表持锁。
	return nil
}
