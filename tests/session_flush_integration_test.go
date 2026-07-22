package tests

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/neko233-com/db233-go/pkg/db233"
)

func prepareSessionFlushTable(t *testing.T, db *db233.Db, playerPrefix string) func() {
	t.Helper()
	if err := setupBatchFindTable(db); err != nil {
		t.Fatalf("创建 test_batch_find: %v", err)
	}
	return func() {
		_, _ = db.DataSource.Exec("DELETE FROM test_batch_find WHERE playerId LIKE ?", playerPrefix+"%")
	}
}

func TestSessionFlush_MergedPersistsToDB(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()
	defer prepareSessionFlushTable(t, db, "mf_")()

	SaveEntityCacheSettings(t)
	SaveCacheableEntityRegistry(t)
	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &TestBatchFindEntity{}})
	db233.GetEntityCacheSettings().ApplyFull(db233.EntityCacheSettings{
		Enabled:                  true,
		SessionFlushIntervalMs:   0,
		SessionFlushMergeByTable: true,
		SessionFlushMaxWorkers:   4,
	})

	repo := db233.NewBaseCrudRepository(db)
	sr := db233.NewSessionRepository(repo)
	defer sr.Stop()

	const n = 30
	for i := 0; i < n; i++ {
		pid := fmt.Sprintf("mf_%d", i)
		s, err := sr.OpenSession(pid, []db233.IDbEntity{&TestBatchFindEntity{}})
		if err != nil {
			t.Fatalf("OpenSession %s: %v", pid, err)
		}
		if err := s.Put(&TestBatchFindEntity{PlayerID: pid, Name: "merged", Level: i}); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	if err := sr.FlushAllDirty(); err != nil {
		t.Fatalf("FlushAllDirty: %v", err)
	}

	for i := 0; i < n; i++ {
		pid := fmt.Sprintf("mf_%d", i)
		got, err := repo.FindById(pid, &TestBatchFindEntity{})
		if err != nil {
			t.Fatalf("FindById %s: %v", pid, err)
		}
		if got == nil {
			t.Fatalf("missing row %s", pid)
		}
		e := got.(*TestBatchFindEntity)
		if e.Level != i {
			t.Fatalf("level mismatch %s: %d", pid, e.Level)
		}
	}
}

func TestSessionFlush_ShutdownFlushAll(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()
	defer prepareSessionFlushTable(t, db, "sd_")()

	SaveEntityCacheSettings(t)
	SaveCacheableEntityRegistry(t)
	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &TestBatchFindEntity{}})
	db233.GetEntityCacheSettings().ApplyFull(db233.EntityCacheSettings{
		Enabled:                     true,
		SessionFlushIntervalMs:      0,
		SessionFlushMergeByTable:    true,
		ShutdownFlushMaxWorkers:     4,
		ShutdownFlushWaveIntervalMs: 5,
	})

	repo := db233.NewBaseCrudRepository(db)
	sr := db233.NewSessionRepository(repo)
	defer sr.Stop()

	for i := 0; i < 50; i++ {
		pid := fmt.Sprintf("sd_%d", i)
		s, _ := sr.OpenSession(pid, []db233.IDbEntity{&TestBatchFindEntity{}})
		_ = s.Put(&TestBatchFindEntity{PlayerID: pid, Name: "shutdown", Level: 99})
	}

	if err := sr.FlushAll(); err != nil {
		t.Fatalf("FlushAll: %v", err)
	}

	got, err := repo.FindById("sd_25", &TestBatchFindEntity{})
	if err != nil || got == nil {
		t.Fatalf("FlushAll persist failed: %v", err)
	}
}

func TestSessionFlush_ConcurrentCloseSession(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()
	defer prepareSessionFlushTable(t, db, "cc_")()

	SaveEntityCacheSettings(t)
	SaveCacheableEntityRegistry(t)
	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &TestBatchFindEntity{}})
	db233.GetEntityCacheSettings().ApplyFull(db233.EntityCacheSettings{
		Enabled:                true,
		SessionFlushIntervalMs: 0,
		SessionFlushMaxWorkers: 4,
	})

	repo := db233.NewBaseCrudRepository(db)
	sr := db233.NewSessionRepository(repo)
	defer sr.Stop()

	const n = 40
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		pid := fmt.Sprintf("cc_%d", i)
		ids[i] = pid
		s, _ := sr.OpenSession(pid, []db233.IDbEntity{&TestBatchFindEntity{}})
		_ = s.Put(&TestBatchFindEntity{PlayerID: pid, Name: "close", Level: 7})
	}

	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for _, pid := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := sr.CloseSession(id); err != nil {
				errCh <- err
			}
		}(pid)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("CloseSession: %v", err)
	}

	for _, pid := range ids {
		got, err := repo.FindById(pid, &TestBatchFindEntity{})
		if err != nil || got == nil {
			t.Fatalf("missing after close %s", pid)
		}
	}
}

func TestWriteBuffer_IntegrationDedupAndFlush(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()
	defer prepareSessionFlushTable(t, db, "wb_int")()

	SaveCrudPerformanceSettings(t)
	db233.GetCrudPerformanceSettings().ApplyFull(db233.CrudPerformanceSettings{
		WriteBufferEnabled:         true,
		WriteBufferMaxBatchSize:    5,
		WriteBufferFlushIntervalMs: 100,
		BatchUpsertChunkSize:       200,
	})

	repo := db233.NewBaseCrudRepository(db)
	defer repo.Close()
	e := &TestBatchFindEntity{PlayerID: "wb_int", Name: "v1", Level: 1}
	if err := repo.SaveBuffered(e); err != nil {
		t.Fatal(err)
	}
	e.Name = "v2"
	e.Level = 2
	if err := repo.SaveBuffered(e); err != nil {
		t.Fatal(err)
	}
	if err := repo.FlushWriteBuffer(); err != nil {
		t.Fatal(err)
	}

	got, err := repo.FindById("wb_int", &TestBatchFindEntity{})
	if err != nil {
		t.Fatal(err)
	}
	row := got.(*TestBatchFindEntity)
	if row.Level != 2 || row.Name != "v2" {
		t.Fatalf("dedup flush got %+v", row)
	}
}

func TestSessionFlush_PeriodicTick(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()
	defer prepareSessionFlushTable(t, db, "tick")()

	SaveEntityCacheSettings(t)
	SaveCacheableEntityRegistry(t)
	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &TestBatchFindEntity{}})
	db233.GetEntityCacheSettings().ApplyFull(db233.EntityCacheSettings{
		Enabled:                       true,
		SessionFlushIntervalMs:        80,
		SessionFlushIntervalJitterPct: 0,
		SessionFlushMergeByTable:      true,
	})

	repo := db233.NewBaseCrudRepository(db)
	sr := db233.NewSessionRepository(repo)
	defer sr.Stop()

	s, _ := sr.OpenSession("tick1", []db233.IDbEntity{&TestBatchFindEntity{}})
	_ = s.Put(&TestBatchFindEntity{PlayerID: "tick1", Name: "tick", Level: 3})

	time.Sleep(200 * time.Millisecond)

	got, err := repo.FindById("tick1", &TestBatchFindEntity{})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("periodic flush should persist")
	}
	if s.DirtyCount() != 0 {
		t.Fatalf("dirty should clear, count=%d", s.DirtyCount())
	}
}
