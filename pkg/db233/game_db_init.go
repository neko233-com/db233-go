package db233

import (
	"fmt"
)

// GameDbOptions 游戏逻辑服数据库初始化选项（启动时一次性配置，运行期不变）。
type GameDbOptions struct {
	// PerformanceConfigPath 性能配置 JSON 路径（可选）。
	PerformanceConfigPath string

	// LocalJournalPath 本地 WAL 目录（云 DB 不可用时数据落盘）。
	LocalJournalPath string

	// EnableLocalJournal 是否启用 WAL（默认 true）。
	EnableLocalJournal bool

	// EnableWriteBuffer 是否启用写缓冲（默认 true）。
	EnableWriteBuffer bool

	// EntityTypes 需注册到类型表的所有玩家实体（WAL 回放 + 自动建表）。
	EntityTypes []IDbEntity
}

// DefaultGameDbOptions 默认游戏服 DB 选项。
func DefaultGameDbOptions() GameDbOptions {
	return GameDbOptions{
		EnableLocalJournal: true,
		EnableWriteBuffer:  true,
	}
}

// InitGameDb 游戏逻辑服 DB 一站式初始化：配置、连接池、WAL、实体注册。
func InitGameDb(db *Db, dbConfig *DbConnectionConfig, opts GameDbOptions) error {
	if db == nil {
		return fmt.Errorf("db 不能为 nil")
	}

	perf := GetCrudPerformanceSettings()
	if opts.PerformanceConfigPath != "" {
		if err := perf.LoadFromFile(opts.PerformanceConfigPath); err != nil {
			return fmt.Errorf("加载性能配置失败: %w", err)
		}
	}

	settings := perf.Snapshot()
	RegisterDbForConnectionPool(db)

	for _, entity := range opts.EntityTypes {
		GetEntityTypeRegistry().Register(entity)
		cm := GetCrudManagerInstance()
		cm.AutoInitEntity(entity)
	}

	if dbConfig != nil {
		db.EnableFaultTolerance(dbConfig)
		if db.FaultTolerantMgr != nil {
			db.FaultTolerantMgr.SetNeverDropFailedOps(true)
			if opts.LocalJournalPath != "" {
				db.FaultTolerantMgr.SetPersistPath(opts.LocalJournalPath)
			}
		}
	}

	repo := NewBaseCrudRepository(db)

	if opts.EnableWriteBuffer {
		_ = perf.Set("writeBufferEnabled", true)
	}

	if opts.EnableLocalJournal {
		journalPath := opts.LocalJournalPath
		if journalPath == "" {
			journalPath = settings.LocalJournalPath
		}
		journal := NewLocalWriteJournal(journalPath, repo)
		repo.SetWriteJournal(journal)
		db.WriteJournal = journal
		journal.Start()
	}

	// 启动时回放 WAL + 失败操作
	if db.WriteJournal != nil {
		db.WriteJournal.ReplayAll()
	}
	if db.FaultTolerantMgr != nil {
		go db.FaultTolerantMgr.RetryFailedOperationsNow()
	}

	LogInfo("游戏 DB 初始化完成: WAL=%v, WriteBuffer=%v", opts.EnableLocalJournal, opts.EnableWriteBuffer)
	return nil
}
