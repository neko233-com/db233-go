package db233

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"
)

var gameDbInitMu sync.Mutex

// GameDbOptions 游戏逻辑服数据库初始化选项（启动时一次性配置，运行期不变）。
type GameDbOptions struct {
	// DatabaseGeneration 标识当前逻辑数据库代次；清库/重建后必须更换。
	// 空值仅保留兼容行为，生产环境必须配置（例如 data_epoch.EpochId）。
	DatabaseGeneration string

	// PerformanceConfigPath 性能配置 JSON 路径（可选）。
	PerformanceConfigPath string

	// LocalJournalPath 本地 WAL 目录（云 DB 不可用时数据落盘）。
	LocalJournalPath string

	// EnableLocalJournal 是否启用 WAL（默认 true）。
	EnableLocalJournal bool

	// EnableWriteBuffer 是否启用写缓冲（默认 true）。
	EnableWriteBuffer bool

	// EntityTypes 需注册到类型表的所有玩家实体（WAL 回放、预热与 Session）。
	// 建表/迁移应在 InitGameDb 前显式调用 Db.AutoMigrateSchema，并检查返回值。
	EntityTypes []IDbEntity

	// CacheableEntities 可缓存的 XxxEntity 白名单（Session L1 + 延迟刷写）。
	CacheableEntities []CacheableEntitySpec

	// EnableEntityCache 是否启用 Session 实体缓存（默认 true）。
	EnableEntityCache bool

	// AllowWarmupFailure 允许冷启动预热失败后继续初始化。
	// 默认 false（严格）；仅当调用方明确接受首次请求延迟/失败时开启。
	AllowWarmupFailure bool

	// WarmupTimeout 限制 InitGameDb 的 Ping/Prepare/Query 预热 I/O。
	// 0 使用 DefaultWarmupTimeout；负数非法。
	WarmupTimeout time.Duration
}

// DefaultGameDbOptions 默认游戏服 DB 选项。
func DefaultGameDbOptions() GameDbOptions {
	return GameDbOptions{
		EnableLocalJournal: true,
		EnableWriteBuffer:  true,
		EnableEntityCache:  true,
	}
}

// InitGameDb 游戏逻辑服 DB 一站式初始化：配置、连接池、WAL、实体注册、Session 缓存。
// 同一 Db 只能成功初始化一次；并发重复调用会串行化并 fail-fast。
func InitGameDb(db *Db, dbConfig *DbConnectionConfig, opts GameDbOptions) (sessionRepo *SessionRepository, resultErr error) {
	if db == nil {
		return nil, fmt.Errorf("db 不能为 nil")
	}
	if opts.WarmupTimeout < 0 {
		return nil, NewValidationException("WarmupTimeout 不能为负数")
	}
	// Init 会修改进程级性能配置与实体注册表。全程串行才能保证失败回滚
	// 不覆盖另一个 Db 正在提交的全局状态。
	gameDbInitMu.Lock()
	defer gameDbInitMu.Unlock()
	// 与 Db.Close/代次轮换共用生命周期互斥，避免初始化一半时被关闭或重复初始化。
	db.rotationMu.Lock()
	defer db.rotationMu.Unlock()
	db.resourceMu.Lock()
	closing := db.closing || db.closingState.Load()
	alreadyInitialized := db.FaultTolerantMgr != nil || db.WriteJournal != nil || db.SessionRepo != nil
	db.resourceMu.Unlock()
	if closing {
		return nil, ErrCrudRepositoryClosed
	}
	if alreadyInitialized {
		return nil, NewValidationException("同一 Db 不允许重复 InitGameDb")
	}
	if db.isDatabaseGenerationUnavailable() {
		return nil, ErrDatabaseGenerationBlocked
	}

	previousPerformance := GetCrudPerformanceSettings().Snapshot()
	previousEntityCache := GetEntityCacheSettings().Snapshot()
	cacheRegistry := GetCacheableEntityRegistry()
	previousCacheRegistry := cacheRegistry.Snapshot()
	registrationSnapshot := snapshotGameDbRegistrations(opts)
	db.generationMu.RLock()
	previousGeneration := db.databaseGeneration
	previousGenerationErr := db.generationErr
	previousUnavailable := db.generationUnavailable.Load()
	db.generationMu.RUnlock()
	var manager *FaultTolerantManager
	var journal *LocalWriteJournal
	var repo *BaseCrudRepository
	var createdSessionRepo *SessionRepository
	registeredPool := false
	warmupStarted := false
	initialized := false
	defer func() {
		if initialized {
			return
		}
		var cleanupErrors []error
		if createdSessionRepo != nil {
			createdSessionRepo.Stop()
		}
		if warmupStarted && db.DataSource != nil {
			if err := GetPreparedStmtCache().RemoveDBStrict(db.DataSource); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("回滚关闭预热 Stmt: %w", err))
			}
		}
		if repo != nil {
			if err := repo.Close(); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("关闭部分初始化 Repository: %w", err))
			}
		}
		if journal != nil {
			if err := journal.StopStrict(); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("关闭部分初始化 WAL: %w", err))
			}
		}
		if manager != nil {
			if err := manager.StopStrict(); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("关闭部分初始化容错管理器: %w", err))
			}
		}
		if registeredPool {
			UnregisterDbForConnectionPool(db)
		}
		db.resourceMu.Lock()
		if db.SessionRepo == createdSessionRepo {
			db.SessionRepo = nil
		}
		if db.WriteJournal == journal {
			db.WriteJournal = nil
		}
		if db.FaultTolerantMgr == manager {
			db.FaultTolerantMgr = nil
		}
		db.resourceMu.Unlock()
		GetCrudPerformanceSettings().ApplyFull(previousPerformance)
		GetEntityCacheSettings().ApplyFull(previousEntityCache)
		cacheRegistry.Restore(previousCacheRegistry)
		registrationSnapshot.restore()
		db.generationMu.Lock()
		db.databaseGeneration = previousGeneration
		db.generationErr = previousGenerationErr
		db.generationUnavailable.Store(previousUnavailable)
		db.generationMu.Unlock()
		resultErr = errors.Join(resultErr, errors.Join(cleanupErrors...))
	}()

	if err := db.configureDatabaseGeneration(opts.DatabaseGeneration); err != nil {
		return nil, fmt.Errorf("配置 DatabaseGeneration: %w", err)
	}

	perf := GetCrudPerformanceSettings()
	if opts.PerformanceConfigPath != "" {
		data, err := os.ReadFile(opts.PerformanceConfigPath)
		if err != nil {
			return nil, fmt.Errorf("加载性能配置失败: %w", err)
		}
		if err := perf.LoadFromJSON(data); err != nil {
			return nil, fmt.Errorf("解析性能配置失败: %w", err)
		}
		if err := GetEntityCacheSettings().LoadFromJSON(data); err != nil {
			return nil, fmt.Errorf("解析 entityCache 配置失败: %w", err)
		}
	}

	settings := perf.Snapshot()
	repo = NewBaseCrudRepository(db)

	if dbConfig != nil {
		manager = NewFaultTolerantManager(db, dbConfig)
		manager.SetNeverDropFailedOps(true)
		if opts.LocalJournalPath != "" {
			if err := manager.SetPersistPathStrict(opts.LocalJournalPath); err != nil {
				return nil, err
			}
		}
		if err := manager.ConfigureDatabaseGeneration(opts.DatabaseGeneration); err != nil {
			return nil, fmt.Errorf("配置失败操作 DatabaseGeneration: %w", err)
		}
	}

	if opts.EnableLocalJournal {
		journalPath := opts.LocalJournalPath
		if journalPath == "" {
			journalPath = settings.LocalJournalPath
		}
		journal = NewLocalWriteJournal(journalPath, repo)
		if err := journal.ConfigureDatabaseGeneration(opts.DatabaseGeneration); err != nil {
			return nil, fmt.Errorf("配置 WAL DatabaseGeneration: %w", err)
		}
		repo.SetWriteJournal(journal)
	}

	RegisterDbForConnectionPool(db)
	registeredPool = true

	for _, entity := range opts.EntityTypes {
		if err := GetEntityTypeRegistry().RegisterStrict(entity); err != nil {
			return nil, fmt.Errorf("注册实体类型: %w", err)
		}
		cm := GetCrudManagerInstance()
		cm.AutoInitEntity(entity)
	}

	if len(opts.CacheableEntities) > 0 {
		if err := cacheRegistry.RegisterBatchStrict(opts.CacheableEntities); err != nil {
			return nil, fmt.Errorf("注册可缓存实体: %w", err)
		}
	} else {
		for _, entity := range opts.EntityTypes {
			if err := cacheRegistry.RegisterStrict(CacheableEntitySpec{Prototype: entity}); err != nil {
				return nil, fmt.Errorf("注册可缓存实体: %w", err)
			}
		}
	}

	if opts.EnableEntityCache {
		if err := GetEntityCacheSettings().Set("enabled", true); err != nil {
			return nil, fmt.Errorf("启用 EntityCache: %w", err)
		}
	} else {
		if err := GetEntityCacheSettings().Set("enabled", false); err != nil {
			return nil, fmt.Errorf("停用 EntityCache: %w", err)
		}
	}

	if err := perf.Set("writeBufferEnabled", opts.EnableWriteBuffer); err != nil {
		return nil, fmt.Errorf("配置 WriteBuffer: %w", err)
	}

	if manager != nil {
		db.resourceMu.Lock()
		if db.closing || db.closingState.Load() {
			db.resourceMu.Unlock()
			return nil, ErrCrudRepositoryClosed
		}
		if db.FaultTolerantMgr != nil && db.FaultTolerantMgr != manager {
			db.resourceMu.Unlock()
			return nil, NewValidationException("Db 已启用容错管理器")
		}
		db.FaultTolerantMgr = manager
		db.resourceMu.Unlock()
	}
	if journal != nil {
		db.resourceMu.Lock()
		db.WriteJournal = journal
		db.resourceMu.Unlock()
	}

	// 启动后台协程前同步完成恢复；失败则清理所有部分初始化资源并 fail-fast。
	if journal != nil {
		if _, _, err := journal.ReplayAllStrict(); err != nil {
			return nil, fmt.Errorf("启动回放 WAL: %w", err)
		}
	}
	if manager != nil {
		if err := manager.RetryFailedOperationsNowStrict(); err != nil {
			return nil, fmt.Errorf("启动回放失败操作: %w", err)
		}
		if err := manager.StartStrict(); err != nil {
			return nil, fmt.Errorf("启动容错管理器: %w", err)
		}
	}
	if journal != nil {
		if err := journal.StartStrict(); err != nil {
			return nil, fmt.Errorf("启动 WAL: %w", err)
		}
	}

	createdSessionRepo = NewSessionRepository(repo)
	sessionRepo = createdSessionRepo
	db.resourceMu.Lock()
	db.SessionRepo = createdSessionRepo
	db.resourceMu.Unlock()

	warmupTimeout := opts.WarmupTimeout
	if warmupTimeout == 0 {
		warmupTimeout = DefaultWarmupTimeout
	}
	warmupCtx, cancelWarmup := context.WithTimeout(context.Background(), warmupTimeout)
	warmupStarted = true
	warmupErr := WarmGameDbContext(warmupCtx, db, opts.EntityTypes)
	cancelWarmup()
	if warmupErr != nil {
		if !opts.AllowWarmupFailure {
			return nil, fmt.Errorf("冷启动预热失败: %w", warmupErr)
		}
		LogWarn("冷启动预热失败（已显式允许继续）: %s", safeErrorForLog(warmupErr))
	}

	LogInfo("游戏 DB 初始化完成: WAL=%v, WriteBuffer=%v, EntityCache=%v",
		opts.EnableLocalJournal, opts.EnableWriteBuffer, opts.EnableEntityCache)
	initialized = true
	return sessionRepo, nil
}

type entityFactoryRegistrationState struct {
	value  func() IDbEntity
	exists bool
}

type entityTypeRegistrationState struct {
	value  reflect.Type
	exists bool
}

type boolRegistrationState struct {
	value  bool
	exists bool
}

type stringRegistrationState struct {
	value  string
	exists bool
}

type stringSliceRegistrationState struct {
	value  []string
	exists bool
}

type gameDbRegistrationSnapshot struct {
	registryFactories map[string]entityFactoryRegistrationState
	registryTypes     map[string]entityTypeRegistrationState
	crudMetadata      map[reflect.Type]boolRegistrationState
	crudPrimaryKeys   map[string]stringSliceRegistrationState
	crudColumns       map[string]stringSliceRegistrationState
	crudPrimaryCache  map[reflect.Type]stringRegistrationState
}

func snapshotGameDbRegistrations(opts GameDbOptions) gameDbRegistrationSnapshot {
	snapshot := gameDbRegistrationSnapshot{
		registryFactories: make(map[string]entityFactoryRegistrationState),
		registryTypes:     make(map[string]entityTypeRegistrationState),
		crudMetadata:      make(map[reflect.Type]boolRegistrationState),
		crudPrimaryKeys:   make(map[string]stringSliceRegistrationState),
		crudColumns:       make(map[string]stringSliceRegistrationState),
		crudPrimaryCache:  make(map[reflect.Type]stringRegistrationState),
	}

	prototypes := make([]IDbEntity, 0, len(opts.EntityTypes)+len(opts.CacheableEntities))
	prototypes = append(prototypes, opts.EntityTypes...)
	for _, spec := range opts.CacheableEntities {
		prototypes = append(prototypes, spec.Prototype)
	}
	registry := GetEntityTypeRegistry()
	registry.mu.RLock()
	for _, prototype := range prototypes {
		if isNilStrictValue(prototype) {
			continue
		}
		name := EntityTypeName(prototype)
		if _, captured := snapshot.registryFactories[name]; captured {
			continue
		}
		factory, factoryExists := registry.factories[name]
		registeredType, typeExists := registry.types[name]
		snapshot.registryFactories[name] = entityFactoryRegistrationState{value: factory, exists: factoryExists}
		snapshot.registryTypes[name] = entityTypeRegistrationState{value: registeredType, exists: typeExists}
	}
	registry.mu.RUnlock()

	crud := GetCrudManagerInstance()
	tables := make(map[string]struct{})
	types := make(map[reflect.Type]struct{})
	for _, prototype := range opts.EntityTypes {
		if isNilStrictValue(prototype) {
			continue
		}
		entityType := canonicalEntityType(reflect.TypeOf(prototype))
		if entityType == nil {
			continue
		}
		types[entityType] = struct{}{}
		tables[crud.GetTableName(entityType)] = struct{}{}
	}
	crud.mu.RLock()
	for entityType := range types {
		metadata, metadataExists := crud.metadataClassSet[entityType]
		primary, primaryExists := crud.typeToPrimaryKeyColumnCache[entityType]
		snapshot.crudMetadata[entityType] = boolRegistrationState{value: metadata, exists: metadataExists}
		snapshot.crudPrimaryCache[entityType] = stringRegistrationState{value: primary, exists: primaryExists}
	}
	for table := range tables {
		primaryKeys, primaryExists := crud.tableNamePkColNameListMap[table]
		columns, columnsExist := crud.tableNameToColNameMap[table]
		snapshot.crudPrimaryKeys[table] = stringSliceRegistrationState{value: append([]string(nil), primaryKeys...), exists: primaryExists}
		snapshot.crudColumns[table] = stringSliceRegistrationState{value: append([]string(nil), columns...), exists: columnsExist}
	}
	crud.mu.RUnlock()
	return snapshot
}

func (snapshot gameDbRegistrationSnapshot) restore() {
	registry := GetEntityTypeRegistry()
	registry.mu.Lock()
	for name, state := range snapshot.registryFactories {
		if state.exists {
			registry.factories[name] = state.value
		} else {
			delete(registry.factories, name)
		}
	}
	for name, state := range snapshot.registryTypes {
		if state.exists {
			registry.types[name] = state.value
		} else {
			delete(registry.types, name)
		}
	}
	registry.mu.Unlock()

	crud := GetCrudManagerInstance()
	crud.mu.Lock()
	for entityType, state := range snapshot.crudMetadata {
		if state.exists {
			crud.metadataClassSet[entityType] = state.value
		} else {
			delete(crud.metadataClassSet, entityType)
		}
	}
	for entityType, state := range snapshot.crudPrimaryCache {
		if state.exists {
			crud.typeToPrimaryKeyColumnCache[entityType] = state.value
		} else {
			delete(crud.typeToPrimaryKeyColumnCache, entityType)
		}
	}
	for table, state := range snapshot.crudPrimaryKeys {
		if state.exists {
			crud.tableNamePkColNameListMap[table] = append([]string(nil), state.value...)
		} else {
			delete(crud.tableNamePkColNameListMap, table)
		}
	}
	for table, state := range snapshot.crudColumns {
		if state.exists {
			crud.tableNameToColNameMap[table] = append([]string(nil), state.value...)
		} else {
			delete(crud.tableNameToColNameMap, table)
		}
	}
	crud.mu.Unlock()
}
