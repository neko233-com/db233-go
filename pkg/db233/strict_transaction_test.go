package db233

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

var (
	strictHookCalls      atomic.Int64
	strictSerializeCalls atomic.Int64
	strictAfterLoadHook  func()
	strictBeforeSaveHook func()
)

type strictContractEntity struct {
	ID      string         `db:"id" primary_key:"true"`
	Score   int            `db:"score"`
	Note    *string        `db:"note"`
	Payload map[string]int `db:"payload"`
}

func (*strictContractEntity) TableName() string { return "strict_contract_entity" }

func (*strictContractEntity) SerializeBeforeSaveDb() {
	strictSerializeCalls.Add(1)
}

func (*strictContractEntity) DeserializeAfterLoadDb() {
	strictHookCalls.Add(1)
}

type strictReentrantEntity struct {
	ID string `db:"id" primary_key:"true"`
}

func (*strictReentrantEntity) TableName() string      { return "strict_reentrant_entity" }
func (*strictReentrantEntity) SerializeBeforeSaveDb() {}
func (*strictReentrantEntity) DeserializeAfterLoadDb() {
	if strictAfterLoadHook != nil {
		strictAfterLoadHook()
	}
}

type strictAutoIDEntity struct {
	ID             int64  `db:"id" primary_key:"true" auto_increment:"true"`
	Name           string `db:"name"`
	SerializeCalls int    `db:"-"`
}

func (*strictAutoIDEntity) TableName() string { return "strict_auto_id_entity" }

func (e *strictAutoIDEntity) SerializeBeforeSaveDb() {
	e.SerializeCalls++
}

func (*strictAutoIDEntity) DeserializeAfterLoadDb() {}

type strictNamedBool bool

type StrictEmbeddedDTOFields struct {
	Alias   string          `db:"alias"`
	Enabled strictNamedBool `db:"enabled"`
}

// strictEmbeddedDTO 故意不实现 IDbEntity，用于验证严格查询支持普通 DTO。
type strictEmbeddedDTO struct {
	*StrictEmbeddedDTOFields
}

type serialContractEntity struct {
	ID string `db:"id" primary_key:"true"`
}

func (*serialContractEntity) TableName() string { return "serial_contract_entity" }

func (*serialContractEntity) SerializeBeforeSaveDb() {
	if strictBeforeSaveHook != nil {
		strictBeforeSaveHook()
	}
}

func (*serialContractEntity) DeserializeAfterLoadDb() {}

func stringPointer(value string) *string { return &value }

func equalStringPointers(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func applyStrictTestSettings(t testing.TB, fast, prepared bool, batchSize int) {
	t.Helper()
	manager := GetCrudPerformanceSettings()
	previous := manager.Snapshot()
	current := previous
	current.EnableFastOrmScan = fast
	current.EnablePreparedStmtCache = prepared
	if batchSize > 0 {
		current.BatchUpsertChunkSize = batchSize
	}
	manager.ApplyFull(current)
	t.Cleanup(func() {
		manager.ApplyFull(previous)
	})
}

func applyStrictFindChunkSize(t testing.TB, chunkSize int) {
	t.Helper()
	manager := GetCrudPerformanceSettings()
	previous := manager.Snapshot()
	current := previous
	current.FindByIdsChunkSize = chunkSize
	manager.ApplyFull(current)
	t.Cleanup(func() {
		manager.ApplyFull(previous)
	})
}

func newStrictTestDb(t testing.TB, state *scriptedDBState) *Db {
	t.Helper()
	return NewDb(openScriptedDB(t, state), 0, nil)
}

func TestStrictQueryContract(t *testing.T) {
	queryErr := errors.New("strict query failed")
	rowErr := errors.New("strict rows iteration failed")
	closeErr := errors.New("strict rows close failed")

	t.Run("all-or-error failures", func(t *testing.T) {
		tests := []struct {
			name      string
			fast      bool
			steps     []scriptedStep
			params    [][]any
			wantCause error
		}{
			{
				name: "query",
				steps: []scriptedStep{{
					kind:     "query",
					queryErr: queryErr,
				}},
				params:    [][]any{{"p1"}},
				wantCause: queryErr,
			},
			{
				name: "fast scan",
				fast: true,
				steps: []scriptedStep{{
					kind:    "query",
					columns: []string{"id", "score"},
					rows:    [][]driver.Value{{"p1", "not-an-int"}},
				}},
				params: [][]any{{"p1"}},
			},
			{
				name: "legacy conversion",
				steps: []scriptedStep{{
					kind:    "query",
					columns: []string{"id", "payload"},
					rows:    [][]driver.Value{{"p1", []byte("{invalid-json")}},
				}},
				params: [][]any{{"p1"}},
			},
			{
				name: "rows err after partial row",
				steps: []scriptedStep{{
					kind:     "query",
					columns:  []string{"id", "score"},
					rows:     [][]driver.Value{{"p1", int64(1)}},
					rowErrAt: 1,
					rowErr:   rowErr,
				}},
				params:    [][]any{{"p1"}},
				wantCause: rowErr,
			},
			{
				name: "close",
				steps: []scriptedStep{{
					kind:     "query",
					columns:  []string{"id"},
					closeErr: closeErr,
				}},
				params:    [][]any{{"p1"}},
				wantCause: closeErr,
			},
			{
				name: "second parameter group",
				steps: []scriptedStep{
					{
						kind:    "query",
						columns: []string{"id", "score"},
						rows:    [][]driver.Value{{"p1", int64(1)}},
					},
					{kind: "query", queryErr: queryErr},
				},
				params:    [][]any{{"p1"}, {"p2"}},
				wantCause: queryErr,
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				applyStrictTestSettings(t, test.fast, false, 0)
				state := newScriptedDBState(test.steps...)
				db := newStrictTestDb(t, state)
				got, err := db.ExecuteQueryStrictContext(context.Background(), "SELECT * FROM strict_contract_entity WHERE id = ?", test.params, &strictContractEntity{})
				if err == nil {
					t.Fatal("expected strict query error")
				}
				if got != nil {
					t.Fatalf("strict failure returned partial results: %#v", got)
				}
				if test.wantCause != nil && !errors.Is(err, test.wantCause) {
					t.Fatalf("error does not preserve cause %v: %v", test.wantCause, err)
				}
			})
		}
	})

	t.Run("fast and legacy compatibility", func(t *testing.T) {
		for _, fast := range []bool{false, true} {
			mode := "legacy"
			if fast {
				mode = "fast"
			}
			t.Run(mode, func(t *testing.T) {
				for _, valueCase := range []struct {
					name     string
					note     driver.Value
					wantNote *string
				}{
					{name: "NULL pointer", note: nil},
					{name: "non-NULL pointer", note: []byte("hello"), wantNote: stringPointer("hello")},
				} {
					t.Run(valueCase.name, func(t *testing.T) {
						applyStrictTestSettings(t, fast, false, 0)
						strictHookCalls.Store(0)
						state := newScriptedDBState(scriptedStep{
							kind:    "query",
							columns: []string{"id", "score", "note", "payload", "future_column"},
							rows: [][]driver.Value{{
								"p1", nil, valueCase.note, []byte(`{"wins":3}`), "ignored",
							}},
						})
						db := newStrictTestDb(t, state)
						repository := NewStrictCrudRepository(db)
						entity, err := repository.FindByIdContext(context.Background(), "p1", &strictContractEntity{})
						if err != nil {
							t.Fatalf("strict find failed: %v", err)
						}
						got, ok := entity.(*strictContractEntity)
						if !ok {
							t.Fatalf("unexpected entity type: %T", entity)
						}
						if got.Score != 0 || !equalStringPointers(got.Note, valueCase.wantNote) || got.Payload["wins"] != 3 {
							t.Fatalf("NULL/pointer/unmapped compatibility mismatch: %#v", got)
						}
						if calls := strictHookCalls.Load(); calls != 1 {
							t.Fatalf("DeserializeAfterLoadDb calls=%d, want 1", calls)
						}
					})
				}
			})
		}
	})

	t.Run("plain DTO embedded pointer and named bool", func(t *testing.T) {
		for _, fast := range []bool{false, true} {
			mode := "legacy"
			if fast {
				mode = "fast"
			}
			t.Run(mode, func(t *testing.T) {
				applyStrictTestSettings(t, fast, false, 0)
				state := newScriptedDBState(scriptedStep{
					kind:    "query",
					columns: []string{"alias", "enabled", "future_column"},
					rows: [][]driver.Value{{
						[]byte("primary"), []byte("1"), "ignored",
					}},
				})
				db := newStrictTestDb(t, state)
				rows, err := db.ExecuteQueryStrictContext(
					context.Background(),
					"SELECT alias, enabled, future_column FROM strict_dto",
					nil,
					&strictEmbeddedDTO{},
				)
				if err != nil {
					t.Fatalf("strict DTO query failed: %v", err)
				}
				if len(rows) != 1 {
					t.Fatalf("rows=%d, want 1", len(rows))
				}
				got, ok := rows[0].(*strictEmbeddedDTO)
				if !ok || got.StrictEmbeddedDTOFields == nil {
					t.Fatalf("embedded pointer was not initialized: %#v", rows[0])
				}
				if got.Alias != "primary" || !bool(got.Enabled) {
					t.Fatalf("strict DTO mapping mismatch: %#v", got)
				}
			})
		}
	})

	t.Run("unmapped column does not allocate embedded pointer", func(t *testing.T) {
		for _, fast := range []bool{false, true} {
			mode := "legacy"
			if fast {
				mode = "fast"
			}
			t.Run(mode, func(t *testing.T) {
				applyStrictTestSettings(t, fast, false, 0)
				state := newScriptedDBState(scriptedStep{
					kind:    "query",
					columns: []string{"future_column"},
					rows:    [][]driver.Value{{"ignored"}},
				})
				db := newStrictTestDb(t, state)
				rows, err := db.ExecuteQueryStrictContext(
					context.Background(),
					"SELECT future_column FROM strict_dto",
					nil,
					&strictEmbeddedDTO{},
				)
				if err != nil {
					t.Fatalf("strict DTO query failed: %v", err)
				}
				if len(rows) != 1 {
					t.Fatalf("rows=%d, want 1", len(rows))
				}
				got := rows[0].(*strictEmbeddedDTO)
				if got.StrictEmbeddedDTOFields != nil {
					t.Fatalf("unmapped column allocated embedded pointer: %#v", got)
				}
			})
		}
	})

	t.Run("scan plan cache keys use concrete type identity", func(t *testing.T) {
		column := "strict_cache_collision_value"
		stringDTO := reflect.StructOf([]reflect.StructField{{
			Name: "Value",
			Type: reflect.TypeOf(""),
			Tag:  reflect.StructTag(`db:"strict_cache_collision_value"`),
		}})
		intDTO := reflect.StructOf([]reflect.StructField{{
			Name: "Value",
			Type: reflect.TypeOf(int(0)),
			Tag:  reflect.StructTag(`db:"strict_cache_collision_value"`),
		}})

		stringPlan, err := GetOrmScanPlanCache().GetStrictPlan(reflect.New(stringDTO).Interface(), []string{column})
		if err != nil {
			t.Fatalf("build string DTO plan: %v", err)
		}
		intPlan, err := GetOrmScanPlanCache().GetStrictPlan(reflect.New(intDTO).Interface(), []string{column})
		if err != nil {
			t.Fatalf("build int DTO plan: %v", err)
		}
		if stringPlan.entityType != stringDTO || intPlan.entityType != intDTO || stringPlan == intPlan {
			t.Fatalf("scan plan cache collision: string=%v int=%v", stringPlan.entityType, intPlan.entityType)
		}
		if _, err := GetOrmScanPlanCache().GetPlan(reflect.New(stringDTO).Interface(), []string{column}); err == nil {
			t.Fatal("legacy scan plan unexpectedly stopped requiring Entity metadata")
		}
	})

	t.Run("multi-level pointer target is rejected before query", func(t *testing.T) {
		state := newScriptedDBState()
		db := newStrictTestDb(t, state)
		var doublePointer **strictContractEntity
		rows, err := db.ExecuteQueryStrictContext(context.Background(), "SELECT id", nil, doublePointer)
		if err == nil || rows != nil {
			t.Fatalf("double-pointer strict query was not rejected: rows=%#v err=%v", rows, err)
		}
		typedRows, err := ExecuteQueryTypedStrict[**strictContractEntity](db, context.Background(), "SELECT id")
		if err == nil || typedRows != nil {
			t.Fatalf("double-pointer typed query was not rejected: rows=%#v err=%v", typedRows, err)
		}
		if state.countCalls("query") != 0 {
			t.Fatalf("invalid target reached driver: %#v", state.snapshotCalls())
		}
	})

	t.Run("mapping and close errors are both preserved", func(t *testing.T) {
		for _, fast := range []bool{false, true} {
			mode := "legacy"
			if fast {
				mode = "fast"
			}
			t.Run(mode, func(t *testing.T) {
				applyStrictTestSettings(t, fast, false, 0)
				closeFailure := errors.New("combined close failure")
				state := newScriptedDBState(scriptedStep{
					kind:     "query",
					columns:  []string{"score"},
					rows:     [][]driver.Value{{[]byte("not-an-int")}},
					closeErr: closeFailure,
				})
				db := newStrictTestDb(t, state)
				rows, err := db.ExecuteQueryStrictContext(
					context.Background(),
					"SELECT score FROM strict_contract_entity",
					nil,
					&strictContractEntity{},
				)
				if err == nil || rows != nil {
					t.Fatalf("combined failure returned rows=%#v err=%v", rows, err)
				}
				if !errors.Is(err, strconv.ErrSyntax) {
					t.Fatalf("mapping cause was lost: %v", err)
				}
				if !errors.Is(err, closeFailure) {
					t.Fatalf("close cause was lost: %v", err)
				}
			})
		}
	})

	t.Run("FindByIds is all-or-error across chunks", func(t *testing.T) {
		applyStrictTestSettings(t, false, false, 0)
		applyStrictFindChunkSize(t, 1)
		strictHookCalls.Store(0)
		state := newScriptedDBState(
			scriptedStep{
				kind:    "query",
				columns: []string{"id", "score"},
				rows:    [][]driver.Value{{"p1", int64(1)}},
			},
			scriptedStep{kind: "query", queryErr: queryErr},
		)
		repository := NewStrictCrudRepository(newStrictTestDb(t, state))
		entities, err := repository.FindByIdsContext(context.Background(), []any{"p1", "p2"}, &strictContractEntity{})
		if !errors.Is(err, queryErr) {
			t.Fatalf("second chunk query error lost: %v", err)
		}
		if entities != nil {
			t.Fatalf("chunk failure returned partial entities: %#v", entities)
		}
		if calls := strictHookCalls.Load(); calls != 0 {
			t.Fatalf("hook called before all chunks succeeded: %d", calls)
		}
	})

	t.Run("Columns error is strict", func(t *testing.T) {
		applyStrictTestSettings(t, false, false, 0)
		state := newScriptedDBState(scriptedStep{kind: "query", columns: []string{"id"}})
		db := newStrictTestDb(t, state)
		rows, err := db.DataSource.QueryContext(context.Background(), "SELECT id FROM strict_contract_entity")
		if err != nil {
			t.Fatalf("query rows: %v", err)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close rows before mapper: %v", err)
		}
		mapped, err := OrmHandlerInstance.ormBatchStrict(rows, &strictContractEntity{})
		if err == nil {
			t.Fatal("expected Columns error for already closed rows")
		}
		if mapped != nil {
			t.Fatalf("Columns failure returned results: %#v", mapped)
		}
	})

	t.Run("typed strict value and pointer targets", func(t *testing.T) {
		applyStrictTestSettings(t, false, false, 0)
		newState := func() *scriptedDBState {
			return newScriptedDBState(scriptedStep{
				kind:    "query",
				columns: []string{"id", "score"},
				rows:    [][]driver.Value{{"typed", int64(7)}},
			})
		}

		valueRows, err := ExecuteQueryTypedStrict[strictContractEntity](
			newStrictTestDb(t, newState()),
			context.Background(),
			"SELECT id, score FROM strict_contract_entity WHERE id = ?",
			"typed",
		)
		if err != nil || len(valueRows) != 1 || valueRows[0].ID != "typed" || valueRows[0].Score != 7 {
			t.Fatalf("value target mismatch: rows=%#v err=%v", valueRows, err)
		}

		pointerRows, err := ExecuteQueryTypedStrict[*strictContractEntity](
			newStrictTestDb(t, newState()),
			context.Background(),
			"SELECT id, score FROM strict_contract_entity WHERE id = ?",
			"typed",
		)
		if err != nil || len(pointerRows) != 1 || pointerRows[0] == nil || pointerRows[0].ID != "typed" || pointerRows[0].Score != 7 {
			t.Fatalf("pointer target mismatch: rows=%#v err=%v", pointerRows, err)
		}
	})

	t.Run("standard error chain", func(t *testing.T) {
		cause := errors.New("driver sentinel")
		err := NewQueryExceptionWithCause(cause, "wrapped query")
		if !errors.Is(err, cause) {
			t.Fatalf("errors.Is cannot reach cause: %v", err)
		}
		var queryException *QueryException
		if !errors.As(err, &queryException) {
			t.Fatalf("errors.As cannot identify QueryException: %T", err)
		}
	})
}

func TestTransactionLifecycleContract(t *testing.T) {
	t.Run("begin remains usable until commit", func(t *testing.T) {
		state := newScriptedDBState(scriptedStep{kind: "exec", queryContains: "UPDATE"})
		tm := NewTransactionManager(newStrictTestDb(t, state))
		ctx := context.Background()
		if err := tm.BeginContext(ctx); err != nil {
			t.Fatalf("begin: %v", err)
		}
		beginCtx := state.snapshotBeginContext()
		if beginCtx == nil {
			t.Fatal("driver did not capture Begin context")
		}
		select {
		case <-beginCtx.Done():
			t.Fatalf("Begin context was canceled before terminal operation: %v", beginCtx.Err())
		default:
		}
		if _, err := tm.ExecContext(ctx, "UPDATE strict_contract_entity SET score = ?", 1); err != nil {
			t.Fatalf("exec after begin: %v", err)
		}
		if err := tm.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
		if tm.IsActive() {
			t.Fatal("manager remains active after commit")
		}
		calls := state.snapshotCalls()
		if len(calls) != 3 || calls[0].kind != "begin" || calls[1].kind != "exec" || calls[2].kind != "commit" {
			t.Fatalf("unexpected transaction calls: %#v", calls)
		}
		if calls[0].txID == 0 || calls[1].txID != calls[0].txID || calls[2].txID != calls[0].txID {
			t.Fatalf("operations did not use one transaction: %#v", calls)
		}
	})

	t.Run("caller cancellation remains classifiable", func(t *testing.T) {
		state := newScriptedDBState()
		tm := NewTransactionManager(newStrictTestDb(t, state))
		ctx, cancel := context.WithCancel(context.Background())
		if err := tm.BeginContext(ctx); err != nil {
			t.Fatalf("begin: %v", err)
		}
		cancel()
		waitForScriptedCalls(t, state, "rollback", 1)
		err := tm.Commit()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("commit error does not preserve cancellation: %v", err)
		}
		if tm.IsActive() {
			t.Fatal("manager remains active after canceled transaction terminal call")
		}
	})

	t.Run("caller deadline remains classifiable", func(t *testing.T) {
		state := newScriptedDBState()
		tm := NewTransactionManager(newStrictTestDb(t, state))
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		if err := tm.BeginContext(ctx); err != nil {
			t.Fatalf("begin: %v", err)
		}
		<-ctx.Done()
		waitForScriptedCalls(t, state, "rollback", 1)
		err := tm.Commit()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("commit error does not preserve deadline: %v", err)
		}
		if tm.IsActive() {
			t.Fatal("manager remains active after deadline terminal call")
		}
	})

	for _, terminal := range []string{"commit", "rollback"} {
		t.Run(terminal+" failure resets manager", func(t *testing.T) {
			terminalErr := fmt.Errorf("%s failed", terminal)
			state := newScriptedDBState()
			if terminal == "commit" {
				state.commitErr = terminalErr
			} else {
				state.rollbackErr = terminalErr
			}
			tm := NewTransactionManager(newStrictTestDb(t, state))
			if err := tm.BeginContext(context.Background()); err != nil {
				t.Fatalf("begin: %v", err)
			}
			var err error
			if terminal == "commit" {
				err = tm.Commit()
			} else {
				err = tm.Rollback()
			}
			if !errors.Is(err, terminalErr) {
				t.Fatalf("terminal error cause lost: %v", err)
			}
			if tm.IsActive() || tm.tx != nil || tm.txCtx != nil || tm.cancel != nil {
				t.Fatalf("manager state not reset after %s failure", terminal)
			}
		})
	}

	t.Run("options do not leak", func(t *testing.T) {
		state := newScriptedDBState()
		tm := NewTransactionManager(newStrictTestDb(t, state))
		custom := TransactionOptions{Isolation: sql.LevelSerializable, ReadOnly: true, Timeout: time.Minute}
		if err := tm.BeginContext(context.Background(), custom); err != nil {
			t.Fatalf("first begin: %v", err)
		}
		if err := tm.Rollback(); err != nil {
			t.Fatalf("first rollback: %v", err)
		}
		if err := tm.BeginContext(context.Background()); err != nil {
			t.Fatalf("second begin: %v", err)
		}
		if tm.timeout != defaultTransactionTimeout || tm.isolation != sql.LevelDefault || tm.readOnly {
			t.Fatalf("options leaked: timeout=%v isolation=%v readOnly=%v", tm.timeout, tm.isolation, tm.readOnly)
		}
		if err := tm.Rollback(); err != nil {
			t.Fatalf("second rollback: %v", err)
		}
		calls := state.snapshotCalls()
		var begins []scriptedCall
		for _, call := range calls {
			if call.kind == "begin" {
				begins = append(begins, call)
			}
		}
		if len(begins) != 2 || !begins[0].options.ReadOnly || begins[1].options.ReadOnly || begins[1].options.Isolation != driver.IsolationLevel(sql.LevelDefault) {
			t.Fatalf("unexpected begin options: %#v", begins)
		}
	})
}

func TestTransactionHelperContract(t *testing.T) {
	t.Run("callback and rollback errors are joined", func(t *testing.T) {
		callbackErr := errors.New("callback failed")
		rollbackErr := errors.New("rollback failed")
		state := newScriptedDBState()
		state.rollbackErr = rollbackErr
		tm := NewTransactionManager(newStrictTestDb(t, state))
		err := tm.ExecuteInTransactionContext(context.Background(), func(*TransactionManager) error {
			return callbackErr
		})
		if !errors.Is(err, callbackErr) || !errors.Is(err, rollbackErr) {
			t.Fatalf("joined error lost a cause: %v", err)
		}
		if tm.IsActive() || state.countCalls("rollback") != 1 {
			t.Fatalf("rollback/reset mismatch: active=%v calls=%d", tm.IsActive(), state.countCalls("rollback"))
		}
	})

	t.Run("panic rolls back and is rethrown unchanged", func(t *testing.T) {
		panicValue := errors.New("panic sentinel")
		state := newScriptedDBState()
		tm := NewTransactionManager(newStrictTestDb(t, state))
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			_ = tm.ExecuteInTransactionContext(context.Background(), func(*TransactionManager) error {
				panic(panicValue)
			})
		}()
		if recovered != panicValue {
			t.Fatalf("panic changed: got=%v want=%v", recovered, panicValue)
		}
		if tm.IsActive() || state.countCalls("rollback") != 1 {
			t.Fatalf("panic rollback/reset mismatch: active=%v calls=%d", tm.IsActive(), state.countCalls("rollback"))
		}
	})
}

func TestTransactionCrudRepositoryContract(t *testing.T) {
	t.Run("active and transaction identity guards", func(t *testing.T) {
		state := newScriptedDBState()
		tm := NewTransactionManager(newStrictTestDb(t, state))
		if _, err := tm.CrudRepository(); err == nil {
			t.Fatal("repository obtained before Begin")
		}
		if err := tm.BeginContext(context.Background()); err != nil {
			t.Fatalf("first begin: %v", err)
		}
		oldRepository, err := tm.CrudRepository()
		if err != nil {
			t.Fatalf("get active repository: %v", err)
		}
		if err := tm.Commit(); err != nil {
			t.Fatalf("first commit: %v", err)
		}
		if _, err := oldRepository.FindAllContext(context.Background(), &strictContractEntity{}); err == nil {
			t.Fatal("repository remained usable after commit")
		}
		if state.countCalls("query") != 0 {
			t.Fatal("stale repository reached the driver after commit")
		}
		if err := tm.BeginContext(context.Background()); err != nil {
			t.Fatalf("second begin: %v", err)
		}
		if _, err := oldRepository.FindAllContext(context.Background(), &strictContractEntity{}); err == nil {
			t.Fatal("old repository rebound to a new transaction")
		}
		if state.countCalls("query") != 0 {
			t.Fatal("old repository reached the new transaction")
		}
		if _, err := tm.CrudRepository(); err != nil {
			t.Fatalf("new transaction repository unavailable: %v", err)
		}
		if err := tm.Rollback(); err != nil {
			t.Fatalf("second rollback: %v", err)
		}
	})

	t.Run("typed nil entities are rejected before driver access", func(t *testing.T) {
		state := newScriptedDBState()
		tm := NewTransactionManager(newStrictTestDb(t, state))
		if err := tm.BeginContext(context.Background()); err != nil {
			t.Fatalf("begin: %v", err)
		}
		repository, err := tm.CrudRepository()
		if err != nil {
			t.Fatalf("get repository: %v", err)
		}
		var entity *strictContractEntity
		if err := repository.SaveContext(context.Background(), entity); err == nil {
			t.Fatal("typed nil SaveContext unexpectedly succeeded")
		}
		if err := repository.SaveBatchUpsertContext(context.Background(), []IDbEntity{entity}); err == nil {
			t.Fatal("typed nil batch unexpectedly succeeded")
		}
		if _, err := repository.DeleteByIdContext(context.Background(), "id", entity); err == nil {
			t.Fatal("typed nil DeleteByIdContext unexpectedly succeeded")
		}
		if state.countCalls("exec") != 0 {
			t.Fatalf("typed nil validation reached driver: %#v", state.snapshotCalls())
		}
		if err := tm.Rollback(); err != nil {
			t.Fatalf("rollback: %v", err)
		}
	})

	t.Run("repository operations are serialized across handles", func(t *testing.T) {
		driverEntered := make(chan struct{}, 1)
		driverRelease := make(chan struct{})
		driverReleased := false
		defer func() {
			if !driverReleased {
				close(driverRelease)
			}
		}()
		state := newScriptedDBState(
			scriptedStep{
				kind:          "exec",
				queryContains: "INSERT",
				driverEntered: driverEntered,
				driverRelease: driverRelease,
			},
			scriptedStep{kind: "exec", queryContains: "INSERT"},
		)
		tm := NewTransactionManager(newStrictTestDb(t, state))
		if err := tm.BeginContext(context.Background()); err != nil {
			t.Fatalf("begin: %v", err)
		}
		firstRepository, err := tm.CrudRepository()
		if err != nil {
			t.Fatalf("get first repository: %v", err)
		}
		secondRepository, err := tm.CrudRepository()
		if err != nil {
			t.Fatalf("get second repository: %v", err)
		}

		firstDone := make(chan error, 1)
		go func() {
			firstDone <- firstRepository.SaveContext(context.Background(), &serialContractEntity{ID: "first"})
		}()
		select {
		case <-driverEntered:
		case <-time.After(2 * time.Second):
			t.Fatal("first repository operation did not reach driver")
		}
		if tm.operationMu.TryLock() {
			tm.operationMu.Unlock()
			t.Fatal("repository reached driver without holding manager operation lock")
		}

		close(driverRelease)
		driverReleased = true
		select {
		case err := <-firstDone:
			if err != nil {
				t.Fatalf("first repository operation: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for first repository operation")
		}
		if err := secondRepository.SaveContext(context.Background(), &serialContractEntity{ID: "second"}); err != nil {
			t.Fatalf("second repository operation: %v", err)
		}
		if calls := state.countCalls("exec"); calls != 2 {
			t.Fatalf("repository exec calls=%d, want 2", calls)
		}
		if err := tm.Rollback(); err != nil {
			t.Fatalf("rollback: %v", err)
		}
	})

	t.Run("serialize hook can reenter transaction manager", func(t *testing.T) {
		state := newScriptedDBState(
			scriptedStep{kind: "exec", queryContains: "UPDATE serialize_hook"},
			scriptedStep{kind: "exec", queryContains: "INSERT"},
		)
		tm := NewTransactionManager(newStrictTestDb(t, state))
		if err := tm.BeginContext(context.Background()); err != nil {
			t.Fatalf("begin: %v", err)
		}
		repository, err := tm.CrudRepository()
		if err != nil {
			t.Fatalf("get repository: %v", err)
		}

		var hookErr error
		strictBeforeSaveHook = func() {
			strictBeforeSaveHook = nil
			if !tm.operationMu.TryLock() {
				hookErr = errors.New("SerializeBeforeSaveDb ran while transaction operation lock was held")
				return
			}
			tm.operationMu.Unlock()
			_, hookErr = tm.Exec("UPDATE serialize_hook SET value = 1")
		}
		t.Cleanup(func() { strictBeforeSaveHook = nil })

		if err := repository.SaveContext(context.Background(), &serialContractEntity{ID: "outer"}); err != nil {
			t.Fatalf("save with reentrant serialize hook: %v", err)
		}
		if hookErr != nil {
			t.Fatalf("serialize hook reentry: %v", hookErr)
		}
		if state.countCalls("exec") != 2 {
			t.Fatalf("exec calls=%d, want 2", state.countCalls("exec"))
		}
		if err := tm.Rollback(); err != nil {
			t.Fatalf("rollback: %v", err)
		}
	})

	t.Run("deserialize hook can reenter repository after rows close", func(t *testing.T) {
		state := newScriptedDBState(
			scriptedStep{kind: "query", columns: []string{"id"}, rows: [][]driver.Value{{"outer"}}},
			scriptedStep{kind: "query", columns: []string{"id"}, rows: [][]driver.Value{{"inner"}}},
		)
		tm := NewTransactionManager(newStrictTestDb(t, state))
		if err := tm.BeginContext(context.Background()); err != nil {
			t.Fatalf("begin: %v", err)
		}
		repository, err := tm.CrudRepository()
		if err != nil {
			t.Fatalf("get repository: %v", err)
		}

		var hookErr error
		strictAfterLoadHook = func() {
			strictAfterLoadHook = nil
			done := make(chan error, 1)
			go func() {
				_, nestedErr := repository.FindAllContext(context.Background(), &strictReentrantEntity{})
				done <- nestedErr
			}()
			select {
			case hookErr = <-done:
			case <-time.After(2 * time.Second):
				hookErr = errors.New("nested repository read blocked by deserialize hook lock")
			}
		}
		t.Cleanup(func() { strictAfterLoadHook = nil })

		entities, err := repository.FindAllContext(context.Background(), &strictReentrantEntity{})
		if err != nil || len(entities) != 1 {
			t.Fatalf("outer strict read: count=%d err=%v", len(entities), err)
		}
		if hookErr != nil {
			t.Fatalf("deserialize hook reentry: %v", hookErr)
		}
		if state.countCalls("query") != 2 {
			t.Fatalf("query calls=%d, want 2", state.countCalls("query"))
		}
		if err := tm.Rollback(); err != nil {
			t.Fatalf("rollback: %v", err)
		}
	})

	t.Run("raw exec and savepoint operations honor operation lock", func(t *testing.T) {
		operations := []struct {
			name    string
			step    scriptedStep
			execute func(*TransactionManager) error
		}{
			{
				name: "Exec",
				step: scriptedStep{kind: "exec", queryContains: "UPDATE raw_exec"},
				execute: func(tm *TransactionManager) error {
					_, err := tm.Exec("UPDATE raw_exec SET value = 1")
					return err
				},
			},
			{
				name: "ExecContext",
				step: scriptedStep{kind: "exec", queryContains: "UPDATE raw_exec_context"},
				execute: func(tm *TransactionManager) error {
					_, err := tm.ExecContext(context.Background(), "UPDATE raw_exec_context SET value = 1")
					return err
				},
			},
			{
				name: "Query",
				step: scriptedStep{kind: "query", queryContains: "SELECT raw_query", columns: []string{"value"}},
				execute: func(tm *TransactionManager) error {
					rows, err := tm.Query("SELECT raw_query")
					if err != nil {
						return err
					}
					return rows.Close()
				},
			},
			{
				name: "QueryContext",
				step: scriptedStep{kind: "query", queryContains: "SELECT raw_query_context", columns: []string{"value"}},
				execute: func(tm *TransactionManager) error {
					rows, err := tm.QueryContext(context.Background(), "SELECT raw_query_context")
					if err != nil {
						return err
					}
					return rows.Close()
				},
			},
			{
				name: "Savepoint",
				step: scriptedStep{kind: "exec", queryContains: "SAVEPOINT serial_guard"},
				execute: func(tm *TransactionManager) error {
					return tm.Savepoint("serial_guard")
				},
			},
		}

		for _, operation := range operations {
			t.Run(operation.name, func(t *testing.T) {
				driverEntered := make(chan struct{}, 1)
				driverRelease := make(chan struct{})
				driverReleased := false
				defer func() {
					if !driverReleased {
						close(driverRelease)
					}
				}()
				step := operation.step
				step.driverEntered = driverEntered
				step.driverRelease = driverRelease
				state := newScriptedDBState(step)
				tm := NewTransactionManager(newStrictTestDb(t, state))
				if err := tm.BeginContext(context.Background()); err != nil {
					t.Fatalf("begin: %v", err)
				}

				operationDone := make(chan error, 1)
				go func() {
					operationDone <- operation.execute(tm)
				}()
				select {
				case <-driverEntered:
				case <-time.After(2 * time.Second):
					t.Fatalf("%s did not reach driver", operation.name)
				}
				if tm.operationMu.TryLock() {
					tm.operationMu.Unlock()
					t.Fatalf("%s reached driver without holding operation lock", operation.name)
				}

				close(driverRelease)
				driverReleased = true
				select {
				case err := <-operationDone:
					if err != nil {
						t.Fatalf("%s operation: %v", operation.name, err)
					}
				case <-time.After(2 * time.Second):
					t.Fatalf("timed out waiting for %s operation", operation.name)
				}
				if calls := state.countCalls(operation.step.kind); calls != 1 {
					t.Fatalf("%s driver calls=%d, want 1", operation.name, calls)
				}
				if err := tm.Rollback(); err != nil {
					t.Fatalf("rollback: %v", err)
				}
			})
		}
	})

	t.Run("auto increment IDs apply only after successful commit", func(t *testing.T) {
		state := newScriptedDBState(scriptedStep{
			kind: "exec",
			result: scriptedResult{
				lastInsertID: 100,
				rowsAffected: 1,
			},
		})
		tm := NewTransactionManager(newStrictTestDb(t, state))
		if err := tm.BeginContext(context.Background()); err != nil {
			t.Fatalf("begin: %v", err)
		}
		repository, err := tm.CrudRepository()
		if err != nil {
			t.Fatalf("get repository: %v", err)
		}
		entity := &strictAutoIDEntity{Name: "committed"}
		if err := repository.SaveContext(context.Background(), entity); err != nil {
			t.Fatalf("save: %v", err)
		}
		if entity.ID != 0 {
			t.Fatalf("auto ID applied before commit: %d", entity.ID)
		}
		if err := tm.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
		if entity.ID != 100 {
			t.Fatalf("auto ID after commit=%d, want 100", entity.ID)
		}
	})

	t.Run("auto increment failure and rollback do not mutate IDs", func(t *testing.T) {
		lastInsertIDErr := errors.New("last insert ID failed")
		for _, test := range []struct {
			name    string
			result  scriptedResult
			wantErr error
		}{
			{
				name: "LastInsertId error",
				result: scriptedResult{
					lastInsertIDErr: lastInsertIDErr,
					rowsAffected:    1,
				},
				wantErr: lastInsertIDErr,
			},
			{
				name: "rollback after generated ID",
				result: scriptedResult{
					lastInsertID: 200,
					rowsAffected: 1,
				},
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				state := newScriptedDBState(scriptedStep{kind: "exec", result: test.result})
				tm := NewTransactionManager(newStrictTestDb(t, state))
				if err := tm.BeginContext(context.Background()); err != nil {
					t.Fatalf("begin: %v", err)
				}
				repository, err := tm.CrudRepository()
				if err != nil {
					t.Fatalf("get repository: %v", err)
				}
				entity := &strictAutoIDEntity{Name: test.name}
				err = repository.SaveContext(context.Background(), entity)
				if test.wantErr != nil && !errors.Is(err, test.wantErr) {
					t.Fatalf("LastInsertId error cause lost: %v", err)
				}
				if test.wantErr == nil && err != nil {
					t.Fatalf("save: %v", err)
				}
				if err := tm.Rollback(); err != nil {
					t.Fatalf("rollback: %v", err)
				}
				if entity.ID != 0 {
					t.Fatalf("rollback/error retained auto ID: %d", entity.ID)
				}
			})
		}
	})

	t.Run("rollback to savepoint discards later auto ID actions", func(t *testing.T) {
		state := newScriptedDBState(
			scriptedStep{kind: "exec", queryContains: "INSERT", result: scriptedResult{lastInsertID: 100, rowsAffected: 1}},
			scriptedStep{kind: "exec", queryContains: "SAVEPOINT keep"},
			scriptedStep{kind: "exec", queryContains: "INSERT", result: scriptedResult{lastInsertID: 200, rowsAffected: 1}},
			scriptedStep{kind: "exec", queryContains: "SAVEPOINT discard"},
			scriptedStep{kind: "exec", queryContains: "INSERT", result: scriptedResult{lastInsertID: 300, rowsAffected: 1}},
			scriptedStep{kind: "exec", queryContains: "ROLLBACK TO SAVEPOINT keep"},
			scriptedStep{kind: "exec", queryContains: "RELEASE SAVEPOINT keep"},
		)
		tm := NewTransactionManager(newStrictTestDb(t, state))
		if err := tm.BeginContext(context.Background()); err != nil {
			t.Fatalf("begin: %v", err)
		}
		repository, err := tm.CrudRepository()
		if err != nil {
			t.Fatalf("get repository: %v", err)
		}

		committed := &strictAutoIDEntity{Name: "committed"}
		rolledBack := &strictAutoIDEntity{Name: "rolled-back"}
		rolledBackAfterNested := &strictAutoIDEntity{Name: "rolled-back-after-nested"}
		if err := repository.SaveContext(context.Background(), committed); err != nil {
			t.Fatalf("save committed entity: %v", err)
		}
		if err := tm.Savepoint("keep"); err != nil {
			t.Fatalf("create keep savepoint: %v", err)
		}
		if err := repository.SaveContext(context.Background(), rolledBack); err != nil {
			t.Fatalf("save rolled-back entity: %v", err)
		}
		if err := tm.Savepoint("discard"); err != nil {
			t.Fatalf("create discard savepoint: %v", err)
		}
		if err := repository.SaveContext(context.Background(), rolledBackAfterNested); err != nil {
			t.Fatalf("save nested rolled-back entity: %v", err)
		}
		if err := tm.RollbackToSavepoint("keep"); err != nil {
			t.Fatalf("rollback to keep savepoint: %v", err)
		}
		if savepoints := tm.GetSavepoints(); len(savepoints) != 1 || savepoints[0] != "keep" {
			t.Fatalf("savepoints after rollback=%v, want [keep]", savepoints)
		}
		if err := tm.ReleaseSavepoint("keep"); err != nil {
			t.Fatalf("release keep savepoint: %v", err)
		}
		if savepoints := tm.GetSavepoints(); len(savepoints) != 0 {
			t.Fatalf("savepoints after release=%v, want empty", savepoints)
		}
		if err := tm.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
		if committed.ID != 100 {
			t.Fatalf("pre-savepoint ID=%d, want 100", committed.ID)
		}
		if rolledBack.ID != 0 || rolledBackAfterNested.ID != 0 {
			t.Fatalf("rolled-back IDs were applied: first=%d nested=%d", rolledBack.ID, rolledBackAfterNested.ID)
		}
	})

	t.Run("delete upsert chunks and strict read share one transaction", func(t *testing.T) {
		applyStrictTestSettings(t, false, true, 2)
		strictHookCalls.Store(0)
		strictSerializeCalls.Store(0)
		state := newScriptedDBState(
			scriptedStep{kind: "exec", queryContains: "DELETE", result: driver.RowsAffected(2)},
			scriptedStep{kind: "exec", queryContains: "INSERT"},
			scriptedStep{kind: "exec", queryContains: "INSERT"},
			scriptedStep{
				kind:          "query",
				queryContains: "SELECT",
				columns:       []string{"id", "score", "note", "payload"},
				rows: [][]driver.Value{
					{"p1", int64(1), nil, []byte(`{"wins":1}`)},
					{"p2", int64(2), nil, []byte(`{"wins":2}`)},
					{"p3", int64(3), nil, []byte(`{"wins":3}`)},
				},
			},
		)
		tm := NewTransactionManager(newStrictTestDb(t, state))
		if err := tm.BeginContext(context.Background()); err != nil {
			t.Fatalf("begin: %v", err)
		}
		repository, err := tm.CrudRepository()
		if err != nil {
			t.Fatalf("get repository: %v", err)
		}
		affected, err := repository.DeleteByConditionContext(context.Background(), "score >= ?", []any{0}, &strictContractEntity{})
		if err != nil || affected != 2 {
			t.Fatalf("delete: affected=%d err=%v", affected, err)
		}
		entities := []IDbEntity{
			&strictContractEntity{ID: "p1", Score: 1},
			&strictContractEntity{ID: "p2", Score: 2},
			&strictContractEntity{ID: "p3", Score: 3},
		}
		if err := repository.SaveBatchUpsertContext(context.Background(), entities); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		loaded, err := repository.FindByConditionContext(context.Background(), "score >= ?", []any{0}, &strictContractEntity{})
		if err != nil || len(loaded) != 3 {
			t.Fatalf("strict read: count=%d err=%v", len(loaded), err)
		}
		if strictSerializeCalls.Load() != 3 || strictHookCalls.Load() != 3 {
			t.Fatalf("hook counts: serialize=%d deserialize=%d", strictSerializeCalls.Load(), strictHookCalls.Load())
		}
		if err := tm.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}

		calls := state.snapshotCalls()
		transactionID := 0
		operationCount := 0
		for _, call := range calls {
			if call.kind == "begin" {
				transactionID = call.txID
			}
			if call.kind == "exec" || call.kind == "query" {
				operationCount++
				if call.txID == 0 || call.txID != transactionID {
					t.Fatalf("operation escaped transaction %d: %#v", transactionID, call)
				}
			}
		}
		if operationCount != 4 {
			t.Fatalf("operation count=%d, want 4; calls=%#v", operationCount, calls)
		}
		if prepares := state.countCalls("prepare"); prepares != 0 {
			t.Fatalf("transaction repository used DB statement cache: prepare calls=%d", prepares)
		}
	})

	t.Run("second upsert chunk stops and rolls back", func(t *testing.T) {
		applyStrictTestSettings(t, false, false, 2)
		chunkErr := errors.New("second chunk failed")
		state := newScriptedDBState(
			scriptedStep{kind: "exec", queryContains: "INSERT"},
			scriptedStep{kind: "exec", queryContains: "INSERT", execErr: chunkErr},
		)
		tm := NewTransactionManager(newStrictTestDb(t, state))
		if err := tm.BeginContext(context.Background()); err != nil {
			t.Fatalf("begin: %v", err)
		}
		repository, err := tm.CrudRepository()
		if err != nil {
			t.Fatalf("get repository: %v", err)
		}
		entities := []IDbEntity{
			&strictContractEntity{ID: "p1"},
			&strictContractEntity{ID: "p2"},
			&strictContractEntity{ID: "p3"},
		}
		err = repository.SaveBatchUpsertContext(context.Background(), entities)
		if !errors.Is(err, chunkErr) {
			t.Fatalf("chunk error cause lost: %v", err)
		}
		if state.countCalls("exec") != 2 {
			t.Fatalf("unexpected exec count: %d", state.countCalls("exec"))
		}
		if err := tm.Rollback(); err != nil {
			t.Fatalf("rollback: %v", err)
		}
		if state.countCalls("rollback") != 1 || state.countCalls("commit") != 0 {
			t.Fatalf("terminal calls: commit=%d rollback=%d", state.countCalls("commit"), state.countCalls("rollback"))
		}
	})

	t.Run("delete propagates RowsAffected error", func(t *testing.T) {
		rowsAffectedErr := errors.New("rows affected failed")
		state := newScriptedDBState(scriptedStep{
			kind:          "exec",
			queryContains: "DELETE",
			result: scriptedResult{
				rowsAffectedErr: rowsAffectedErr,
			},
		})
		tm := NewTransactionManager(newStrictTestDb(t, state))
		if err := tm.BeginContext(context.Background()); err != nil {
			t.Fatalf("begin: %v", err)
		}
		repository, err := tm.CrudRepository()
		if err != nil {
			t.Fatalf("get repository: %v", err)
		}
		if _, err := repository.DeleteByIdContext(context.Background(), "p1", &strictContractEntity{}); !errors.Is(err, rowsAffectedErr) {
			t.Fatalf("RowsAffected error cause lost: %v", err)
		}
		if err := tm.Rollback(); err != nil {
			t.Fatalf("rollback: %v", err)
		}
	})
}

func TestLegacyTransactionSmoke(t *testing.T) {
	t.Run("legacy auto increment hook remains single-call", func(t *testing.T) {
		applyStrictTestSettings(t, false, false, 0)
		state := newScriptedDBState(scriptedStep{
			kind: "exec",
			result: scriptedResult{
				lastInsertID: 10,
				rowsAffected: 1,
			},
		})
		entity := &strictAutoIDEntity{Name: "legacy"}
		if err := NewBaseCrudRepository(newStrictTestDb(t, state)).SaveBatchUpsert([]IDbEntity{entity}); err != nil {
			t.Fatalf("legacy auto increment upsert: %v", err)
		}
		if entity.SerializeCalls != 1 || entity.ID != 10 {
			t.Fatalf("legacy hook/ID mismatch: calls=%d id=%d", entity.SerializeCalls, entity.ID)
		}
	})

	t.Run("WithTransaction", func(t *testing.T) {
		state := newScriptedDBState(scriptedStep{kind: "exec", queryContains: "UPDATE"})
		db := newStrictTestDb(t, state)
		err := WithTransaction(db, func(tm *TransactionManager) error {
			_, execErr := tm.Exec("UPDATE strict_contract_entity SET score = 1")
			return execErr
		})
		if err != nil {
			t.Fatalf("WithTransaction: %v", err)
		}
		if state.countCalls("commit") != 1 || state.countCalls("rollback") != 0 {
			t.Fatalf("unexpected terminal calls: %#v", state.snapshotCalls())
		}
	})

	t.Run("WithTransaction callback error rolls back", func(t *testing.T) {
		callbackErr := errors.New("legacy callback failed")
		state := newScriptedDBState()
		db := newStrictTestDb(t, state)
		err := WithTransaction(db, func(*TransactionManager) error {
			return callbackErr
		})
		if !errors.Is(err, callbackErr) {
			t.Fatalf("callback error cause lost: %v", err)
		}
		if state.countCalls("rollback") != 1 || state.countCalls("commit") != 0 {
			t.Fatalf("unexpected terminal calls: %#v", state.snapshotCalls())
		}
	})

	t.Run("MigrationManager applyMigration", func(t *testing.T) {
		state := newScriptedDBState(
			scriptedStep{kind: "exec", queryContains: "UPDATE strict_contract_entity"},
			scriptedStep{kind: "exec", queryContains: "INSERT INTO schema_migrations"},
		)
		db := newStrictTestDb(t, state)
		manager := NewMigrationManager(db, "")
		err := manager.applyMigration(Migration{
			Version: 1,
			Name:    "strict_smoke",
			UpSQL:   "UPDATE strict_contract_entity SET score = score",
		}, true)
		if err != nil {
			t.Fatalf("applyMigration: %v", err)
		}
		if state.countCalls("exec") != 2 || state.countCalls("commit") != 1 {
			t.Fatalf("unexpected migration calls: %#v", state.snapshotCalls())
		}
	})

	t.Run("MigrationManager applyMigration failure rolls back", func(t *testing.T) {
		migrationErr := errors.New("migration statement failed")
		state := newScriptedDBState(scriptedStep{
			kind:          "exec",
			queryContains: "UPDATE strict_contract_entity",
			execErr:       migrationErr,
		})
		db := newStrictTestDb(t, state)
		manager := NewMigrationManager(db, "")
		err := manager.applyMigration(Migration{
			Version: 2,
			Name:    "strict_smoke_failure",
			UpSQL:   "UPDATE strict_contract_entity SET score = score",
		}, true)
		if !errors.Is(err, migrationErr) {
			t.Fatalf("migration error cause lost: %v", err)
		}
		if state.countCalls("exec") != 1 || state.countCalls("rollback") != 1 || state.countCalls("commit") != 0 {
			t.Fatalf("unexpected migration failure calls: %#v", state.snapshotCalls())
		}
	})
}

func waitForScriptedCalls(t *testing.T, state *scriptedDBState, kind string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if state.countCalls(kind) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d %s calls; got %d", want, kind, state.countCalls(kind))
}
