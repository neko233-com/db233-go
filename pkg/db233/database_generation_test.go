package db233

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRecoveryGenerationStartupMismatchIsolatesWALAndFailedOperations(t *testing.T) {
	dir := t.TempDir()
	repo := NewBaseCrudRepository(nil)
	GetCrudManagerInstance().AutoInitEntity(&flushTestEntity{})

	oldJournal := NewLocalWriteJournal(dir, repo)
	t.Cleanup(func() { _ = oldJournal.StopStrict() })
	if err := oldJournal.ConfigureDatabaseGeneration("epoch-old"); err != nil {
		t.Fatal(err)
	}
	if _, err := oldJournal.AppendEntities("SaveBatchUpsert", []IDbEntity{
		&flushTestEntity{PlayerID: "old-wal", Name: "must-not-replay"},
	}); err != nil {
		t.Fatal(err)
	}

	oldManager := NewFaultTolerantManager(nil, nil)
	oldManager.SetPersistPath(dir)
	if err := oldManager.ConfigureDatabaseGeneration("epoch-old"); err != nil {
		t.Fatal(err)
	}
	if err := oldManager.RecordFailedOperationStrict(&FailedOperation{
		Operation: "ExecuteUpdate",
		SQL:       "UPDATE old_table SET value = 1",
		TableName: "old_table",
	}); err != nil {
		t.Fatal(err)
	}
	if err := oldJournal.StopStrict(); err != nil {
		t.Fatal(err)
	}
	if err := oldManager.StopStrict(); err != nil {
		t.Fatal(err)
	}

	newJournal := NewLocalWriteJournal(dir, repo)
	t.Cleanup(func() { _ = newJournal.StopStrict() })
	if err := newJournal.ConfigureDatabaseGeneration("epoch-new"); err != nil {
		t.Fatal(err)
	}
	if count, err := newJournal.PendingCount(); err != nil || count != 0 {
		t.Fatalf("新代 WAL pending=%d err=%v", count, err)
	}
	// WAL 与 failed-ops 使用独立 manifest；轮换 WAL 不得提前移动失败操作。
	if _, err := os.Stat(filepath.Join(dir, "failed_operations.json")); err != nil {
		t.Fatalf("WAL 轮换影响 failed-ops: %v", err)
	}

	newManager := NewFaultTolerantManager(nil, nil)
	t.Cleanup(func() { _ = newManager.StopStrict() })
	newManager.SetPersistPath(dir)
	if err := newManager.ConfigureDatabaseGeneration("epoch-new"); err != nil {
		t.Fatal(err)
	}
	if count := newManager.GetFailedOperationCount(); count != 0 {
		t.Fatalf("新代 failed-ops=%d want=0", count)
	}
	quarantined, err := os.ReadDir(filepath.Join(dir, "quarantine"))
	if err != nil {
		t.Fatal(err)
	}
	if len(quarantined) < 2 {
		t.Fatalf("隔离文件=%d want>=2", len(quarantined))
	}
}

func TestDatabaseGenerationTransitionCommitDrainsOldQueues(t *testing.T) {
	cacheSettings := preserveEntityCacheSettings(t)
	settings := DefaultEntityCacheSettings()
	settings.SessionFlushIntervalMs = 0
	cacheSettings.ApplyFull(settings)

	dir := t.TempDir()
	db := NewDb(nil, 0, nil)
	if err := db.configureDatabaseGeneration("epoch-old"); err != nil {
		t.Fatal(err)
	}
	repo := NewBaseCrudRepository(db)
	GetCrudManagerInstance().AutoInitEntity(&flushTestEntity{})
	var flushedMu sync.Mutex
	flushed := make([]IDbEntity, 0, 2)
	repo.SetTestUpsertHook(func(entities []IDbEntity) error {
		flushedMu.Lock()
		flushed = append(flushed, entities...)
		flushedMu.Unlock()
		return nil
	})

	wb := newWriteBufferForGeneration(repo, "epoch-old")
	repo.writeBuffer = wb
	if !db.registerBufferedRepository(repo) {
		t.Fatal("register write buffer failed")
	}
	if queued, err := wb.Enqueue(&flushTestEntity{PlayerID: "old-buffer", Name: "old"}); err != nil || !queued {
		t.Fatalf("enqueue queued=%v err=%v", queued, err)
	}

	journal := NewLocalWriteJournal(dir, repo)
	t.Cleanup(func() { _ = journal.StopStrict() })
	if err := journal.ConfigureDatabaseGeneration("epoch-old"); err != nil {
		t.Fatal(err)
	}
	db.WriteJournal = journal
	repo.SetWriteJournal(journal)

	manager := NewFaultTolerantManager(db, nil)
	t.Cleanup(func() { _ = manager.StopStrict() })
	manager.SetPersistPath(dir)
	if err := manager.ConfigureDatabaseGeneration("epoch-old"); err != nil {
		t.Fatal(err)
	}
	db.FaultTolerantMgr = manager

	sessions := NewSessionRepository(repo)
	defer sessions.Stop()
	session := newPlayerSessionForGeneration("old-player", repo, sessions, "epoch-old")
	session.entities["flush_test_entity"] = &flushTestEntity{PlayerID: "old-player", Name: "cached"}
	session.dirty["flush_test_entity"] = &flushTestEntity{PlayerID: "old-player", Name: "dirty"}
	sessions.sessions.Store("old-player", session)
	sessions.lru.Add("old-player")
	db.SessionRepo = sessions

	transition, err := db.BeginDatabaseGenerationTransition("epoch-new")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wb.Enqueue(&flushTestEntity{PlayerID: "during", Name: "blocked"}); !errors.Is(err, ErrDatabaseGenerationBlocked) {
		_ = transition.Abort()
		t.Fatalf("屏障期间 Enqueue err=%v", err)
	}
	if err := manager.RecordFailedOperationStrict(&FailedOperation{Operation: "ExecuteUpdate", SQL: "UPDATE during"}); !errors.Is(err, ErrDatabaseGenerationBlocked) {
		_ = transition.Abort()
		t.Fatalf("屏障期间 Record err=%v", err)
	}
	if err := transition.Commit(); err != nil {
		t.Fatal(err)
	}

	if got := db.DatabaseGeneration(); got != "epoch-new" {
		t.Fatalf("generation=%q", got)
	}
	if wb.queueSize() != 0 {
		t.Fatalf("旧写缓冲未清空: %d", wb.queueSize())
	}
	flushedMu.Lock()
	flushedCount := len(flushed)
	flushedMu.Unlock()
	if flushedCount != 2 {
		t.Fatalf("旧代 Session/写缓冲刷写数量=%d want=2", flushedCount)
	}
	if count, err := journal.PendingCount(); err != nil || count != 0 {
		t.Fatalf("旧 WAL 未清空: count=%d err=%v", count, err)
	}
	if count := manager.GetFailedOperationCount(); count != 0 {
		t.Fatalf("旧 failed-ops 未清空: %d", count)
	}
	if sessions.OnlineCount() != 0 {
		t.Fatalf("旧 Session 未清空: %d", sessions.OnlineCount())
	}
	if err := session.Put(&flushTestEntity{PlayerID: "old-player", Name: "stale"}); !errors.Is(err, ErrDatabaseGenerationChanged) {
		if !errors.Is(err, ErrSessionRepositoryClosed) {
			t.Fatalf("旧 Session 应失效: %v", err)
		}
	}
	if session.IsLoaded() || session.DirtyCount() != 0 || session.Get(&flushTestEntity{}) != nil || session.NegativeCacheEnabled() {
		t.Fatal("切代后旧 Session 状态 API 未 fail-closed")
	}
	session.mu.RLock()
	if session.entities != nil || session.dirty != nil || session.dirtyPreparationState != nil || session.dirtyPreparationErrors != nil || session.dirtyVersions != nil || session.absentTables != nil {
		session.mu.RUnlock()
		t.Fatal("切代后旧 Session 仍持有对象图")
	}
	session.mu.RUnlock()
}

func TestDatabaseGenerationTransitionBeginFailureRestoresOldQueues(t *testing.T) {
	dir := t.TempDir()
	db := NewDb(nil, 0, nil)
	if err := db.configureDatabaseGeneration("epoch-old"); err != nil {
		t.Fatal(err)
	}
	repo := NewBaseCrudRepository(db)
	GetCrudManagerInstance().AutoInitEntity(&flushTestEntity{})
	journal := NewLocalWriteJournal(dir, repo)
	t.Cleanup(func() { _ = journal.StopStrict() })
	if err := journal.ConfigureDatabaseGeneration("epoch-old"); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.AppendEntities("SaveBatchUpsert", []IDbEntity{
		&flushTestEntity{PlayerID: "keep", Name: "old"},
	}); err != nil {
		t.Fatal(err)
	}
	db.WriteJournal = journal

	transition, err := db.BeginDatabaseGenerationTransition("epoch-new")
	if err == nil || transition != nil {
		if transition != nil {
			_ = transition.Abort()
		}
		t.Fatalf("无数据库连接时排空 WAL 应失败: transition=%v err=%v", transition, err)
	}
	if got := db.DatabaseGeneration(); got != "epoch-old" {
		t.Fatalf("Begin 失败后 generation=%q", got)
	}
	if count, err := journal.PendingCount(); err != nil || count != 1 {
		t.Fatalf("Begin 失败后 WAL pending=%d err=%v", count, err)
	}
	if _, err := journal.AppendEntities("SaveBatchUpsert", []IDbEntity{
		&flushTestEntity{PlayerID: "after-abort", Name: "ok"},
	}); err != nil {
		t.Fatalf("Begin 失败后 WAL admission 未恢复: %v", err)
	}
}

func TestDatabaseGenerationBarrierWaitsForInFlightWriteBufferFlush(t *testing.T) {
	db := NewDb(nil, 0, nil)
	if err := db.configureDatabaseGeneration("epoch-old"); err != nil {
		t.Fatal(err)
	}
	repo := NewBaseCrudRepository(db)
	wb := newWriteBufferForGeneration(repo, "epoch-old")
	repo.writeBuffer = wb
	if !db.registerBufferedRepository(repo) {
		t.Fatal("register write buffer failed")
	}
	if queued, err := wb.Enqueue(&flushTestEntity{PlayerID: "in-flight", Name: "old"}); err != nil || !queued {
		t.Fatalf("enqueue queued=%v err=%v", queued, err)
	}

	entered := make(chan struct{})
	releaseFlush := make(chan struct{})
	var once sync.Once
	repo.SetTestUpsertHook(func([]IDbEntity) error {
		once.Do(func() { close(entered) })
		<-releaseFlush
		return nil
	})
	flushDone := make(chan error, 1)
	go func() { flushDone <- wb.Flush() }()
	<-entered

	transitionCh := make(chan *DatabaseGenerationTransition, 1)
	errCh := make(chan error, 1)
	go func() {
		transition, err := db.BeginDatabaseGenerationTransition("epoch-new")
		transitionCh <- transition
		errCh <- err
	}()
	select {
	case <-transitionCh:
		t.Fatal("generation 屏障未等待正在执行的 Flush")
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseFlush)
	if err := <-flushDone; err != nil {
		t.Fatal(err)
	}
	transition := <-transitionCh
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if err := transition.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseGenerationTransitionCommitIsSingleUseAndRotateSameIsIdempotent(t *testing.T) {
	db := NewDb(nil, 0, nil)
	if err := db.configureDatabaseGeneration("epoch-old"); err != nil {
		t.Fatal(err)
	}
	transition, err := db.BeginDatabaseGenerationTransition("epoch-new")
	if err != nil {
		t.Fatal(err)
	}
	if err := transition.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := transition.Commit(); err == nil {
		t.Fatal("transition Commit 重复调用应返回错误")
	}
	if err := transition.Abort(); err == nil {
		t.Fatal("已 Commit 的 transition 不得 Abort")
	}
	if err := db.RotateDatabaseGeneration("epoch-new"); err != nil {
		t.Fatalf("同 generation Rotate 应幂等: %v", err)
	}
	if db.isDatabaseGenerationUnavailable() {
		t.Fatal("同 generation Rotate 后仍处于 fail-closed")
	}
	repo := NewBaseCrudRepository(db)
	wb := newWriteBufferForGeneration(repo, "epoch-new")
	if queued, err := wb.Enqueue(&flushTestEntity{PlayerID: "idempotent", Name: "ok"}); err != nil || !queued {
		t.Fatalf("幂等 Rotate 后写缓冲不可用: queued=%v err=%v", queued, err)
	}
}

func TestDatabaseGenerationTransitionFailClosedOnUnknownCommitOutcome(t *testing.T) {
	db := NewDb(nil, 0, nil)
	if err := db.configureDatabaseGeneration("epoch-old"); err != nil {
		t.Fatal(err)
	}
	transition, err := db.BeginDatabaseGenerationTransition("epoch-new")
	if err != nil {
		t.Fatal(err)
	}
	commitUnknown := errors.New("commit outcome unknown")
	if err := transition.FailClosed(commitUnknown); !errors.Is(err, ErrDatabaseGenerationBlocked) || !errors.Is(err, commitUnknown) {
		t.Fatalf("FailClosed err=%v", err)
	}
	if got := db.DatabaseGeneration(); got != "epoch-old" {
		t.Fatalf("未知结果不得猜测新 generation: %q", got)
	}
	if !db.isDatabaseGenerationUnavailable() {
		t.Fatal("未知 commit 结果后必须保持 fail-closed")
	}
	repo := NewBaseCrudRepository(db)
	wb := newWriteBufferForGeneration(repo, "epoch-old")
	if _, err := wb.Enqueue(&flushTestEntity{PlayerID: "blocked", Name: "blocked"}); !errors.Is(err, ErrDatabaseGenerationBlocked) {
		t.Fatalf("FailClosed 后 Enqueue err=%v", err)
	}
	if err := transition.Abort(); err == nil {
		t.Fatal("FailClosed 后不得 Abort")
	}
	// 运维确认事务未提交后，可显式恢复旧 generation。
	if err := db.RotateDatabaseGeneration("epoch-old"); err != nil {
		t.Fatalf("确认后恢复旧 generation 失败: %v", err)
	}
	if db.isDatabaseGenerationUnavailable() {
		t.Fatal("显式恢复后仍被阻断")
	}
}

func TestDatabaseGenerationRotationFailureStaysFailClosedUntilExplicitRecovery(t *testing.T) {
	goodDir := t.TempDir()
	blockedPath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedPath, []byte("block mkdir"), 0644); err != nil {
		t.Fatal(err)
	}

	db := NewDb(nil, 0, nil)
	if err := db.configureDatabaseGeneration("epoch-old"); err != nil {
		t.Fatal(err)
	}
	repo := NewBaseCrudRepository(db)
	journal := NewLocalWriteJournal(goodDir, repo)
	t.Cleanup(func() { _ = journal.StopStrict() })
	if err := journal.ConfigureDatabaseGeneration("epoch-old"); err != nil {
		t.Fatal(err)
	}
	db.WriteJournal = journal

	transition, err := db.BeginDatabaseGenerationTransition("epoch-new")
	if err != nil {
		t.Fatal(err)
	}
	journal.dir = blockedPath
	commitErr := transition.Commit()
	if !errors.Is(commitErr, ErrDatabaseGenerationBlocked) {
		t.Fatalf("轮换失败 err=%v", commitErr)
	}
	if got := db.DatabaseGeneration(); got != "epoch-old" {
		t.Fatalf("失败后 generation=%q want epoch-old", got)
	}
	if !db.isDatabaseGenerationUnavailable() {
		t.Fatal("轮换失败后未保持 fail-closed")
	}
	wb := newWriteBufferForGeneration(repo, "epoch-old")
	if _, err := wb.Enqueue(&flushTestEntity{PlayerID: "blocked", Name: "blocked"}); !errors.Is(err, ErrDatabaseGenerationBlocked) {
		t.Fatalf("fail-closed 期间 Enqueue err=%v", err)
	}
	if _, err := journal.AppendEntities("SaveBatchUpsert", []IDbEntity{
		&flushTestEntity{PlayerID: "blocked-wal", Name: "blocked"},
	}); !errors.Is(err, ErrDatabaseGenerationBlocked) {
		t.Fatalf("fail-closed 期间 WAL Append err=%v", err)
	}
	if err := db.RotateDatabaseGeneration("epoch-new"); !errors.Is(err, ErrDatabaseGenerationBlocked) {
		t.Fatalf("故障未修复时 Rotate err=%v", err)
	}
	if !db.isDatabaseGenerationUnavailable() {
		t.Fatal("重复失败后意外解除 fail-closed")
	}

	// 运维修复持久化目录后显式重试，才允许解除阻断。
	journal.dir = goodDir
	if err := db.RotateDatabaseGeneration("epoch-new"); err != nil {
		t.Fatalf("显式恢复失败: %v", err)
	}
	if db.isDatabaseGenerationUnavailable() || db.DatabaseGeneration() != "epoch-new" {
		t.Fatal("显式恢复后 generation 状态不正确")
	}
}

func TestCorruptRecoveryFilesAreQuarantinedAndFailClosed(t *testing.T) {
	dir := t.TempDir()
	repo := NewBaseCrudRepository(nil)

	seedJournal := NewLocalWriteJournal(dir, repo)
	t.Cleanup(func() { _ = seedJournal.StopStrict() })
	if err := seedJournal.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pending.ndjson"), []byte("{broken\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := seedJournal.StopStrict(); err != nil {
		t.Fatal(err)
	}
	journal := NewLocalWriteJournal(dir, repo)
	t.Cleanup(func() { _ = journal.StopStrict() })
	if err := journal.ConfigureDatabaseGeneration("epoch"); !errors.Is(err, ErrDatabaseGenerationBlocked) {
		t.Fatalf("损坏 WAL Configure err=%v", err)
	}
	if _, err := journal.AppendEntities("SaveBatchUpsert", []IDbEntity{
		&flushTestEntity{PlayerID: "blocked", Name: "blocked"},
	}); !errors.Is(err, ErrDatabaseGenerationBlocked) {
		t.Fatalf("损坏 WAL 后 Append err=%v", err)
	}

	seedManager := NewFaultTolerantManager(nil, nil)
	seedManager.SetPersistPath(dir)
	if err := seedManager.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	if err := seedManager.StopStrict(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "failed_operations.json"), []byte("[broken"), 0644); err != nil {
		t.Fatal(err)
	}
	manager := NewFaultTolerantManager(nil, nil)
	t.Cleanup(func() { _ = manager.StopStrict() })
	manager.SetPersistPath(dir)
	if err := manager.ConfigureDatabaseGeneration("epoch"); !errors.Is(err, ErrDatabaseGenerationBlocked) {
		t.Fatalf("损坏 failed-ops Configure err=%v", err)
	}
	if err := manager.RecordFailedOperationStrict(&FailedOperation{Operation: "ExecuteUpdate"}); !errors.Is(err, ErrDatabaseGenerationBlocked) {
		t.Fatalf("损坏 failed-ops 后 Record err=%v", err)
	}

	quarantined, err := os.ReadDir(filepath.Join(dir, "quarantine"))
	if err != nil {
		t.Fatal(err)
	}
	if len(quarantined) < 2 {
		t.Fatalf("损坏恢复文件隔离数=%d want>=2", len(quarantined))
	}
}
