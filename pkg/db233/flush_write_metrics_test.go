package db233

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

type flushMetricsEntity struct {
	PlayerID string `db:"playerId" primary_key:"true"`
	Value    int    `db:"value"`
}

func (*flushMetricsEntity) TableName() string       { return "flush_metrics_entity" }
func (*flushMetricsEntity) SerializeBeforeSaveDb()  {}
func (*flushMetricsEntity) DeserializeAfterLoadDb() {}

func TestFlushWriteMetrics_ZeroValueAndCallerSelectedWindow(t *testing.T) {
	var db Db
	zero := db.FlushWriteMetrics()
	if zero.AttemptedSQL != 0 || zero.SucceededSQL != 0 || zero.FailedSQL != 0 {
		t.Fatalf("zero-value counters=%+v", zero.FlushWriteCounters)
	}
	if !zero.StartedAt.IsZero() || zero.Elapsed != 0 || db.AverageFlushWritesPerSecond() != 0 {
		t.Fatalf("zero-value lifetime=%+v", zero)
	}
	if len(zero.BySource) != len(flushWriteSources) {
		t.Fatalf("zero-value source count=%d", len(zero.BySource))
	}
	var nilDB *Db
	if got := nilDB.AverageFlushWritesPerSecond(); got != 0 {
		t.Fatalf("nil Db average=%v", got)
	}

	db.flushWriteMetrics.startedUnixNano.Store(time.Now().Add(-2 * time.Second).UnixNano())
	db.recordFlushWriteAttempt(FlushWriteSourceSession, 2)
	db.recordFlushWriteResult(FlushWriteSourceSession, 2, true)
	db.recordFlushWriteAttempt(FlushWriteSourceSession, 3)
	db.recordFlushWriteResult(FlushWriteSourceSession, 3, false)
	lifetime := db.FlushWriteMetrics()
	if lifetime.AverageAttemptedSQLPerSecond < 0.9 || lifetime.AverageAttemptedSQLPerSecond > 1.1 {
		t.Fatalf("lifetime attempted SQL/s=%v", lifetime.AverageAttemptedSQLPerSecond)
	}
	lifetime.BySource[FlushWriteSourceSession] = FlushWriteCounters{AttemptedSQL: 999}
	immutable := db.FlushWriteMetrics()
	if got := immutable.BySource[FlushWriteSourceSession].AttemptedSQL; got != 2 {
		t.Fatalf("snapshot map mutated collector: %d", got)
	}

	base := time.Unix(100, 0)
	previous := FlushWriteMetricsSnapshot{
		FlushWriteCounters: FlushWriteCounters{
			AttemptedSQL:      10,
			SucceededSQL:      8,
			FailedSQL:         2,
			AttemptedEntities: 20,
			SucceededEntities: 16,
			FailedEntities:    4,
		},
		SampledAt: base,
		BySource: map[FlushWriteSource]FlushWriteCounters{
			FlushWriteSourceSession: {AttemptedSQL: 10, SucceededSQL: 8, FailedSQL: 2},
		},
	}
	current := FlushWriteMetricsSnapshot{
		FlushWriteCounters: FlushWriteCounters{
			AttemptedSQL:      14,
			SucceededSQL:      11,
			FailedSQL:         3,
			AttemptedEntities: 30,
			SucceededEntities: 24,
			FailedEntities:    6,
		},
		SampledAt: base.Add(2 * time.Second),
		BySource: map[FlushWriteSource]FlushWriteCounters{
			FlushWriteSourceSession: {AttemptedSQL: 14, SucceededSQL: 11, FailedSQL: 3},
		},
	}
	rates := current.RateSince(previous)
	if rates.Window != 2*time.Second || rates.AttemptedSQLPerSecond != 2 || rates.SucceededSQLPerSecond != 1.5 || rates.FailedSQLPerSecond != 0.5 {
		t.Fatalf("window rates=%+v", rates)
	}
	if got := rates.BySource[FlushWriteSourceSession].AttemptedSQLPerSecond; got != 2 {
		t.Fatalf("session attempted rate=%v", got)
	}
	if invalid := previous.RateSince(current); invalid.Window != 0 || invalid.AttemptedSQLPerSecond != 0 {
		t.Fatalf("non-positive window=%+v", invalid)
	}
}

func TestFlushWriteMetrics_ConcurrentLowOverheadCounters(t *testing.T) {
	var db Db
	const workers = 64
	const callsPerWorker = 500

	start := make(chan struct{})
	var writers sync.WaitGroup
	writers.Add(workers)
	for worker := 0; worker < workers; worker++ {
		worker := worker
		go func() {
			defer writers.Done()
			<-start
			source := FlushWriteSourceSession
			if worker%2 == 1 {
				source = FlushWriteSourceWriteBuffer
			}
			for call := 0; call < callsPerWorker; call++ {
				db.recordFlushWriteAttempt(source, 2)
				db.recordFlushWriteResult(source, 2, call%4 != 0)
			}
		}()
	}
	close(start)

	stopReader := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			snapshot := db.FlushWriteMetrics()
			if len(snapshot.BySource) != len(flushWriteSources) || math.IsNaN(snapshot.AverageAttemptedSQLPerSecond) {
				t.Errorf("invalid concurrent snapshot=%+v", snapshot)
				return
			}
			select {
			case <-time.After(time.Microsecond):
			case <-stopReader:
				return
			}
		}
	}()
	writers.Wait()
	close(stopReader)
	<-readerDone
	snapshot := db.FlushWriteMetrics()
	wantAttempts := uint64(workers * callsPerWorker)
	wantFailures := uint64(workers * ((callsPerWorker + 3) / 4))
	if snapshot.AttemptedSQL != wantAttempts || snapshot.FailedSQL != wantFailures || snapshot.SucceededSQL != wantAttempts-wantFailures {
		t.Fatalf("concurrent SQL counters=%+v", snapshot.FlushWriteCounters)
	}
	if snapshot.AttemptedEntities != wantAttempts*2 || snapshot.FailedEntities != wantFailures*2 {
		t.Fatalf("concurrent entity counters=%+v", snapshot.FlushWriteCounters)
	}
	if snapshot.StartedAt.IsZero() || snapshot.Elapsed < 0 || math.IsNaN(snapshot.AverageAttemptedSQLPerSecond) {
		t.Fatalf("concurrent lifetime=%+v", snapshot)
	}
}

func TestFlushWriteMetrics_MergedChunksFailuresAndPostExecErrors(t *testing.T) {
	settingsManager := GetCrudPerformanceSettings()
	previousSettings := settingsManager.Snapshot()
	settings := previousSettings
	settings.BatchUpsertChunkSize = 2
	settings.EnablePreparedStmtCache = false
	settingsManager.ApplyFull(settings)
	t.Cleanup(func() { settingsManager.ApplyFull(previousSettings) })

	t.Run("merged SQL counts once and chunks count individually", func(t *testing.T) {
		state := newScriptedDBState(
			scriptedStep{kind: "exec", result: scriptedResult{rowsAffected: 2}},
			scriptedStep{kind: "exec", result: scriptedResult{rowsAffected: 2}},
			scriptedStep{kind: "exec", result: scriptedResult{rowsAffected: 1}},
		)
		db := newStrictTestDb(t, state)
		repo := NewBaseCrudRepository(db)
		entities := make([]IDbEntity, 5)
		for index := range entities {
			entities[index] = &flushMetricsEntity{PlayerID: string(rune('a' + index)), Value: index}
		}
		if err := repo.UpdateBatchUpsert(entities); err != nil {
			t.Fatal(err)
		}
		metrics := db.FlushWriteMetrics()
		if metrics.AttemptedSQL != 3 || metrics.SucceededSQL != 3 || metrics.FailedSQL != 0 {
			t.Fatalf("chunk SQL counters=%+v", metrics.FlushWriteCounters)
		}
		if metrics.AttemptedEntities != 5 || metrics.SucceededEntities != 5 || metrics.FailedEntities != 0 {
			t.Fatalf("chunk entity counters=%+v", metrics.FlushWriteCounters)
		}
		if source := metrics.BySource[FlushWriteSourceManual]; source.AttemptedSQL != 3 || source.AttemptedEntities != 5 {
			t.Fatalf("manual source=%+v", source)
		}
	})

	t.Run("Exec error is failed database pressure", func(t *testing.T) {
		driverErr := errors.New("flush unavailable")
		state := newScriptedDBState(scriptedStep{kind: "exec", execErr: driverErr})
		db := newStrictTestDb(t, state)
		repo := NewBaseCrudRepository(db)
		err := repo.UpdateBatchUpsert([]IDbEntity{
			&flushMetricsEntity{PlayerID: "a", Value: 1},
			&flushMetricsEntity{PlayerID: "b", Value: 2},
		})
		if !errors.Is(err, driverErr) {
			t.Fatalf("error=%v", err)
		}
		metrics := db.FlushWriteMetrics()
		if metrics.AttemptedSQL != 1 || metrics.SucceededSQL != 0 || metrics.FailedSQL != 1 || metrics.FailedEntities != 2 {
			t.Fatalf("failed counters=%+v", metrics.FlushWriteCounters)
		}
	})

	t.Run("RowsAffected error remains DB success", func(t *testing.T) {
		rowsErr := errors.New("rows affected unavailable")
		state := newScriptedDBState(scriptedStep{
			kind:   "exec",
			result: scriptedResult{rowsAffectedErr: rowsErr},
		})
		db := newStrictTestDb(t, state)
		repo := NewBaseCrudRepository(db)
		err := repo.UpdateBatchUpsert([]IDbEntity{&flushMetricsEntity{PlayerID: "a", Value: 1}})
		if !errors.Is(err, rowsErr) {
			t.Fatalf("error=%v", err)
		}
		metrics := db.FlushWriteMetrics()
		if metrics.AttemptedSQL != 1 || metrics.SucceededSQL != 1 || metrics.FailedSQL != 0 || metrics.SucceededEntities != 1 {
			t.Fatalf("post-Exec counters=%+v", metrics.FlushWriteCounters)
		}
	})
}

func TestFlushWriteMetrics_PreExecFailuresAreNotAttempts(t *testing.T) {
	settingsManager := GetCrudPerformanceSettings()
	previousSettings := settingsManager.Snapshot()
	settings := previousSettings
	settings.EnablePreparedStmtCache = true
	settingsManager.ApplyFull(settings)
	t.Cleanup(func() { settingsManager.ApplyFull(previousSettings) })

	prepareErr := errors.New("prepare failed before Exec")
	state := newScriptedDBState()
	state.prepareErr = prepareErr
	db := newStrictTestDb(t, state)
	t.Cleanup(func() { GetPreparedStmtCache().RemoveDB(db.DataSource) })
	repo := NewBaseCrudRepository(db)
	err := repo.UpdateBatchUpsert([]IDbEntity{&flushMetricsEntity{PlayerID: "a", Value: 1}})
	if !errors.Is(err, prepareErr) {
		t.Fatalf("error=%v", err)
	}
	if state.countCalls("exec") != 0 {
		t.Fatalf("Exec calls=%d", state.countCalls("exec"))
	}
	metrics := db.FlushWriteMetrics()
	if metrics.AttemptedSQL != 0 || metrics.SucceededSQL != 0 || metrics.FailedSQL != 0 {
		t.Fatalf("pre-Exec failure counted=%+v", metrics.FlushWriteCounters)
	}
}

func TestFlushWriteMetrics_WALAndBuildFailuresBeforeExecAreNotAttempts(t *testing.T) {
	settingsManager := GetCrudPerformanceSettings()
	previousSettings := settingsManager.Snapshot()
	settings := previousSettings
	settings.EnablePreparedStmtCache = false
	settingsManager.ApplyFull(settings)
	t.Cleanup(func() { settingsManager.ApplyFull(previousSettings) })

	t.Run("stopped WAL", func(t *testing.T) {
		state := newScriptedDBState()
		db := newStrictTestDb(t, state)
		repo := NewBaseCrudRepository(db)
		journal := NewLocalWriteJournal(t.TempDir(), repo)
		if err := journal.StopStrict(); err != nil {
			t.Fatal(err)
		}
		repo.SetWriteJournal(journal)
		err := repo.UpdateBatchUpsert([]IDbEntity{&flushMetricsEntity{PlayerID: "wal", Value: 1}})
		if !errors.Is(err, ErrLocalWriteJournalStopped) {
			t.Fatalf("error=%v", err)
		}
		if state.countCalls("exec") != 0 || db.FlushWriteMetrics().AttemptedSQL != 0 {
			t.Fatalf("WAL pre-Exec failure counted: calls=%d metrics=%+v", state.countCalls("exec"), db.FlushWriteMetrics())
		}
	})

	t.Run("statement build", func(t *testing.T) {
		state := newScriptedDBState()
		db := newStrictTestDb(t, state)
		repo := NewBaseCrudRepository(db)
		err := repo.UpdateBatchUpsert([]IDbEntity{&repositoryUnsafeColumnEntity{ID: "bad", Payload: "value"}})
		if err == nil {
			t.Fatal("expected unsafe identifier failure")
		}
		if state.countCalls("exec") != 0 || db.FlushWriteMetrics().AttemptedSQL != 0 {
			t.Fatalf("build failure counted: calls=%d metrics=%+v", state.countCalls("exec"), db.FlushWriteMetrics())
		}
	})
}

func TestFlushWriteMetrics_ExcludesGenericAndRawWrites(t *testing.T) {
	settingsManager := GetCrudPerformanceSettings()
	previousSettings := settingsManager.Snapshot()
	settings := previousSettings
	settings.EnablePreparedStmtCache = false
	settingsManager.ApplyFull(settings)
	t.Cleanup(func() { settingsManager.ApplyFull(previousSettings) })

	state := newScriptedDBState(
		scriptedStep{kind: "exec", result: scriptedResult{rowsAffected: 1}},
		scriptedStep{kind: "exec", result: scriptedResult{rowsAffected: 1}},
	)
	db := newStrictTestDb(t, state)
	repo := NewBaseCrudRepository(db)
	if err := repo.SaveBatchUpsert([]IDbEntity{&flushMetricsEntity{PlayerID: "generic", Value: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecuteUpdate("UPDATE flush_metrics_entity SET value=? WHERE playerId=?", 2, "generic"); err != nil {
		t.Fatal(err)
	}
	metrics := db.FlushWriteMetrics()
	if metrics.AttemptedSQL != 0 || metrics.SucceededSQL != 0 || metrics.FailedSQL != 0 {
		t.Fatalf("generic/raw writes counted=%+v", metrics.FlushWriteCounters)
	}
	if state.countCalls("exec") != 2 {
		t.Fatalf("actual generic/raw Exec calls=%d, want 2", state.countCalls("exec"))
	}
}

func BenchmarkFlushWriteMetrics_Record(b *testing.B) {
	var db Db
	// Exclude the one-time start timestamp initialization from steady-state cost.
	db.recordFlushWriteAttempt(FlushWriteSourceSession, 1)
	db.recordFlushWriteResult(FlushWriteSourceSession, 1, true)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db.recordFlushWriteAttempt(FlushWriteSourceSession, 100)
		db.recordFlushWriteResult(FlushWriteSourceSession, 100, true)
	}
}
