package db233

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

type strictWarmupEntity struct {
	ID string `db:"id" primary_key:"true"`
}

func (*strictWarmupEntity) TableName() string       { return "strict_warmup_entity" }
func (*strictWarmupEntity) SerializeBeforeSaveDb()  {}
func (*strictWarmupEntity) DeserializeAfterLoadDb() {}

type panicWarmupEntity struct {
	ID string `db:"id" primary_key:"true"`
}

func (*panicWarmupEntity) TableName() string {
	panic(errors.New("private-table-name-canary"))
}
func (*panicWarmupEntity) SerializeBeforeSaveDb()  {}
func (*panicWarmupEntity) DeserializeAfterLoadDb() {}

func configureStrictWarmupTest(t *testing.T, prepare bool) {
	t.Helper()
	settings := preservePerformanceSettingsUnit(t).Snapshot()
	settings.EnableColdStartWarmup = true
	settings.EnablePreparedStmtCache = prepare
	settings.EnableSqlTemplateCache = true
	settings.PoolWarmupRounds = 1
	GetCrudPerformanceSettings().ApplyFull(settings)
}

func TestWarmGameDbContextStrictFailuresAndCancellation(t *testing.T) {
	t.Run("nil inputs", func(t *testing.T) {
		//lint:ignore SA1012 此用例必须验证公开 API 对 nil context 的严格拒绝。
		if err := WarmGameDbContext(nil, nil, nil); err == nil {
			t.Fatal("nil context was accepted")
		}
		if err := WarmGameDbContext(context.Background(), nil, nil); err == nil {
			t.Fatal("nil Db was accepted")
		}
	})

	t.Run("generation lock honors deadline before IO", func(t *testing.T) {
		configureStrictWarmupTest(t, false)
		state := newScriptedDBState()
		db := newStrictTestDb(t, state)
		db.generationMu.Lock()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
		err := WarmGameDbContext(ctx, db, nil)
		cancel()
		db.generationMu.Unlock()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("generation wait error=%v", err)
		}
		if state.countCalls("ping") != 0 {
			t.Fatal("deadline-expired generation wait reached database")
		}
	})

	t.Run("ping honors deadline", func(t *testing.T) {
		configureStrictWarmupTest(t, false)
		state := newScriptedDBState()
		state.pingRelease = make(chan struct{})
		state.pingRespectContext = true
		db := newStrictTestDb(t, state)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
		defer cancel()
		if err := WarmGameDbContext(ctx, db, nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ping cancellation error=%v", err)
		}
	})

	t.Run("prepare cause and cancellation are strict", func(t *testing.T) {
		configureStrictWarmupTest(t, true)
		prepareErr := errors.New("prepare failed")
		state := newScriptedDBState()
		state.prepareErr = prepareErr
		db := newStrictTestDb(t, state)
		if err := WarmGameDbContext(context.Background(), db, []IDbEntity{&strictWarmupEntity{}}); !errors.Is(err, prepareErr) {
			t.Fatalf("prepare cause lost: %v", err)
		}

		blocked := newScriptedDBState()
		blocked.prepareRelease = make(chan struct{})
		blocked.prepareRespectContext = true
		blockedDb := newStrictTestDb(t, blocked)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
		defer cancel()
		if err := WarmGameDbContext(ctx, blockedDb, []IDbEntity{&strictWarmupEntity{}}); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("prepare cancellation error=%v", err)
		}
	})

	t.Run("query and rows close causes are strict", func(t *testing.T) {
		configureStrictWarmupTest(t, false)
		queryErr := errors.New("query failed")
		db := newStrictTestDb(t, newScriptedDBState(scriptedStep{kind: "query", queryErr: queryErr}))
		if err := WarmGameDbContext(context.Background(), db, []IDbEntity{&strictWarmupEntity{}}); !errors.Is(err, queryErr) {
			t.Fatalf("query cause lost: %v", err)
		}

		closeErr := errors.New("rows close failed")
		closed := make(chan struct{}, 1)
		closeDb := newStrictTestDb(t, newScriptedDBState(scriptedStep{
			kind: "query", columns: []string{"id"}, closeErr: closeErr, closeNotify: closed,
		}))
		if err := WarmGameDbContext(context.Background(), closeDb, []IDbEntity{&strictWarmupEntity{}}); !errors.Is(err, closeErr) {
			t.Fatalf("Rows.Close cause lost: %v", err)
		}
		select {
		case <-closed:
		default:
			t.Fatal("warmup query rows were not closed")
		}
	})

	t.Run("business panic is contained and redacted", func(t *testing.T) {
		configureStrictWarmupTest(t, false)
		db := newStrictTestDb(t, newScriptedDBState())
		err := WarmGameDbContext(context.Background(), db, []IDbEntity{&panicWarmupEntity{}})
		if err == nil {
			t.Fatal("TableName panic was swallowed")
		}
		if strings.Contains(err.Error(), "private-table-name-canary") {
			t.Fatalf("panic payload leaked: %v", err)
		}
	})
}

func TestInitGameDbWarmupTimeoutRollsBackResourcesAndStmt(t *testing.T) {
	configureStrictWarmupTest(t, true)
	queryErr := errors.New("warmup query failed")
	state := newScriptedDBState(scriptedStep{kind: "query", queryErr: queryErr})
	db := newStrictTestDb(t, state)
	options := GameDbOptions{
		DatabaseGeneration: "strict-init-epoch",
		EntityTypes:        []IDbEntity{&strictWarmupEntity{}},
		WarmupTimeout:      time.Second,
	}
	sessions, err := InitGameDb(db, nil, options)
	if sessions != nil || !errors.Is(err, queryErr) {
		t.Fatalf("InitGameDb sessions=%v err=%v", sessions, err)
	}
	if db.SessionRepo != nil || db.WriteJournal != nil || db.FaultTolerantMgr != nil {
		t.Fatal("failed InitGameDb published partial resources")
	}
	if _, registered := registeredPoolDbs.Load(db); registered {
		t.Fatal("failed InitGameDb leaked connection-pool registration")
	}
	cache := GetPreparedStmtCache()
	cache.mu.Lock()
	for key := range cache.entries {
		if key.db == db.DataSource {
			cache.mu.Unlock()
			t.Fatal("failed InitGameDb leaked prepared statement cache entry")
		}
	}
	cache.mu.Unlock()
	state.mu.Lock()
	closeCalls := state.stmtCloseCalls
	state.mu.Unlock()
	if closeCalls != 1 {
		t.Fatalf("prepared stmt close calls=%d, want 1", closeCalls)
	}
}

func TestPreparedStmtReleasedThroughDbClose(t *testing.T) {
	state := newScriptedDBState()
	db := newStrictTestDb(t, state)
	_, release, err := GetPreparedStmtCache().AcquireStmtContext(context.Background(), db.DataSource, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	closeCalls := state.stmtCloseCalls
	state.mu.Unlock()
	if closeCalls != 1 {
		t.Fatalf("driver statement close calls=%d, want 1", closeCalls)
	}
}

func TestStrictConvenienceQueriesPropagateAllFailuresAndBindParams(t *testing.T) {
	performance := preservePerformanceSettingsUnit(t)
	settings := performance.Snapshot()
	settings.EnablePreparedStmtCache = false
	performance.ApplyFull(settings)

	t.Run("legacy Query binds parameters through strict implementation", func(t *testing.T) {
		state := newScriptedDBState(scriptedStep{
			kind: "query", columns: []string{"id"}, rows: [][]driver.Value{{int64(7)}},
		})
		db := newStrictTestDb(t, state)
		rows := db.Query("SELECT ? AS id", "bound-value")
		if len(rows) != 1 || rows[0]["id"] != int64(7) {
			t.Fatalf("unexpected rows: %#v", rows)
		}
		calls := state.snapshotCalls()
		if len(calls) != 1 || len(calls[0].args) != 1 || calls[0].args[0].Value != "bound-value" {
			t.Fatalf("Query dropped parameters: %#v", calls)
		}
	})

	t.Run("row iteration failure discards partial maps", func(t *testing.T) {
		rowErr := errors.New("row stream failed")
		db := newStrictTestDb(t, newScriptedDBState(scriptedStep{
			kind: "query", columns: []string{"id"}, rows: [][]driver.Value{{int64(1)}}, rowErrAt: 1, rowErr: rowErr,
		}))
		rows, err := db.QueryStrictContext(context.Background(), "SELECT id")
		if rows != nil || !errors.Is(err, rowErr) {
			t.Fatalf("strict query returned partial rows=%#v err=%v", rows, err)
		}
	})

	t.Run("close failure discards successful maps", func(t *testing.T) {
		closeErr := errors.New("raw rows close failed")
		db := newStrictTestDb(t, newScriptedDBState(scriptedStep{
			kind: "query", columns: []string{"id"}, rows: [][]driver.Value{{int64(1)}}, closeErr: closeErr,
		}))
		rows, err := db.QueryStrictContext(context.Background(), "SELECT id")
		if rows != nil || !errors.Is(err, closeErr) {
			t.Fatalf("strict query returned rows=%#v err=%v", rows, err)
		}
	})

	t.Run("statement failure discards earlier statement results", func(t *testing.T) {
		queryErr := errors.New("second statement failed")
		db := newStrictTestDb(t, newScriptedDBState(
			scriptedStep{kind: "query", columns: []string{"id"}, rows: [][]driver.Value{{int64(1)}}},
			scriptedStep{kind: "query", queryErr: queryErr},
		))
		rows, err := db.ExecuteSqlByStatementStrictContext(
			context.Background(),
			NewQueryStatements([]string{"SELECT 1", "SELECT 2"}, nil),
		)
		if rows != nil || !errors.Is(err, queryErr) {
			t.Fatalf("strict statement returned rows=%#v err=%v", rows, err)
		}
	})

	t.Run("scalar context cancellation is strict", func(t *testing.T) {
		release := make(chan struct{})
		db := newStrictTestDb(t, newScriptedDBState(scriptedStep{
			kind: "query", driverRelease: release, respectContext: true,
		}))
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
		defer cancel()
		value, err := db.QueryToIntStrictContext(ctx, "SELECT blocked")
		if value != 0 || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("scalar cancellation value=%d err=%v", value, err)
		}
	})
}

func TestFailedOperationIDIsOpaqueRandomAndUnique(t *testing.T) {
	manager := NewFaultTolerantManager(nil, nil)
	if err := manager.SetPersistPathStrict(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := manager.ConfigureDatabaseGeneration("opaque-id-epoch"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.StopStrict() })

	operationCanary := "private-operation-derived-id-canary"
	for index := 0; index < 8; index++ {
		if err := manager.RecordFailedOperationStrict(&FailedOperation{Operation: operationCanary}); err != nil {
			t.Fatal(err)
		}
	}
	pattern := regexp.MustCompile(`^ftm_[0-9a-f]{32}$`)
	seen := make(map[string]struct{}, 8)
	manager.failedOpsMutex.RLock()
	defer manager.failedOpsMutex.RUnlock()
	for _, operation := range manager.failedOps {
		if operation == nil || !pattern.MatchString(operation.ID) {
			t.Fatalf("non-opaque operation ID: %#v", operation)
		}
		if strings.Contains(operation.ID, operationCanary) {
			t.Fatalf("operation content leaked into ID: %q", operation.ID)
		}
		if _, duplicate := seen[operation.ID]; duplicate {
			t.Fatalf("duplicate random operation ID: %q", operation.ID)
		}
		seen[operation.ID] = struct{}{}
	}
}
