package db233

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type repositoryIdentifierEntity struct {
	ID             string `db:"id" primary_key:"true"`
	A              string `db:"a"`
	Z              string `db:"z"`
	Table          string `db:"-"`
	SerializeCalls int    `db:"-"`
}

func (e *repositoryIdentifierEntity) TableName() string {
	if e.Table != "" {
		return e.Table
	}
	return "stable_identifier_entity"
}

func (e *repositoryIdentifierEntity) SerializeBeforeSaveDb() { e.SerializeCalls++ }
func (*repositoryIdentifierEntity) DeserializeAfterLoadDb()  {}

type repositoryUnsafeColumnEntity struct {
	ID      string `db:"id" primary_key:"true"`
	Payload string `db:"payload); DROP TABLE victims; --"`
}

func (*repositoryUnsafeColumnEntity) TableName() string       { return "safe_table" }
func (*repositoryUnsafeColumnEntity) SerializeBeforeSaveDb()  {}
func (*repositoryUnsafeColumnEntity) DeserializeAfterLoadDb() {}

type repositoryAutoEntity struct {
	ID   int64  `db:"id" primary_key:"true" auto_increment:"true"`
	Name string `db:"name"`
}

func (*repositoryAutoEntity) TableName() string       { return "repository_auto_entity" }
func (*repositoryAutoEntity) SerializeBeforeSaveDb()  {}
func (*repositoryAutoEntity) DeserializeAfterLoadDb() {}

type repositoryDifferentShapeEntity struct {
	ID    string `db:"id" primary_key:"true"`
	Other string `db:"other"`
}

func (*repositoryDifferentShapeEntity) TableName() string       { return "stable_identifier_entity" }
func (*repositoryDifferentShapeEntity) SerializeBeforeSaveDb()  {}
func (*repositoryDifferentShapeEntity) DeserializeAfterLoadDb() {}

type repositoryBadJSONEntity struct {
	ID      string         `db:"id" primary_key:"true"`
	Payload map[string]any `db:"payload"`
}

func (*repositoryBadJSONEntity) TableName() string       { return "repository_bad_json" }
func (*repositoryBadJSONEntity) SerializeBeforeSaveDb()  {}
func (*repositoryBadJSONEntity) DeserializeAfterLoadDb() {}

type RepositoryEmbeddedFields struct {
	ID   int64  `db:"id" primary_key:"true" auto_increment:"true"`
	Name string `db:"name"`
}

type repositoryNilEmbeddedEntity struct {
	*RepositoryEmbeddedFields
}

func (*repositoryNilEmbeddedEntity) TableName() string       { return "repository_nil_embedded" }
func (*repositoryNilEmbeddedEntity) SerializeBeforeSaveDb()  {}
func (*repositoryNilEmbeddedEntity) DeserializeAfterLoadDb() {}

type repositoryDuplicateColumnEntity struct {
	ID    string `db:"id" primary_key:"true"`
	Alias string `db:"id"`
}

func (*repositoryDuplicateColumnEntity) TableName() string       { return "repository_duplicate_column" }
func (*repositoryDuplicateColumnEntity) SerializeBeforeSaveDb()  {}
func (*repositoryDuplicateColumnEntity) DeserializeAfterLoadDb() {}

type RepositoryRecursiveEntity struct {
	*RepositoryRecursiveEntity
	ID string `db:"id" primary_key:"true"`
}

func (*RepositoryRecursiveEntity) TableName() string       { return "repository_recursive" }
func (*RepositoryRecursiveEntity) SerializeBeforeSaveDb()  {}
func (*RepositoryRecursiveEntity) DeserializeAfterLoadDb() {}

type repositoryBlockingRowsAffectedResult struct {
	entered chan<- struct{}
	release <-chan struct{}
}

func (r repositoryBlockingRowsAffectedResult) LastInsertId() (int64, error) { return 0, nil }

func (r repositoryBlockingRowsAffectedResult) RowsAffected() (int64, error) {
	r.entered <- struct{}{}
	<-r.release
	return 1, nil
}

type repositoryPanicRowsAffectedResult struct{ cause error }

func (r repositoryPanicRowsAffectedResult) LastInsertId() (int64, error) { return 0, nil }
func (r repositoryPanicRowsAffectedResult) RowsAffected() (int64, error) { panic(r.cause) }

func TestRepositoryIdentifierGuards(t *testing.T) {
	t.Run("table injection fails before hook and driver", func(t *testing.T) {
		state := newScriptedDBState()
		repository := NewBaseCrudRepository(newStrictTestDb(t, state))
		entity := &repositoryIdentifierEntity{ID: "1", Table: "users; DROP TABLE users"}
		if err := repository.Save(entity); err == nil {
			t.Fatal("unsafe table name was accepted")
		}
		if entity.SerializeCalls != 0 || state.countCalls("exec") != 0 {
			t.Fatalf("unsafe table reached hook/driver: hooks=%d calls=%#v", entity.SerializeCalls, state.snapshotCalls())
		}
	})

	t.Run("column injection fails before driver", func(t *testing.T) {
		state := newScriptedDBState()
		repository := NewBaseCrudRepository(newStrictTestDb(t, state))
		if err := repository.Save(&repositoryUnsafeColumnEntity{ID: "1"}); err == nil {
			t.Fatal("unsafe db tag was accepted")
		}
		if state.countCalls("exec") != 0 {
			t.Fatalf("unsafe column reached driver: %#v", state.snapshotCalls())
		}
	})

	t.Run("strict and transaction reads reject unsafe metadata", func(t *testing.T) {
		state := newScriptedDBState()
		db := newStrictTestDb(t, state)
		repository := NewBaseCrudRepository(db)
		entityType := &repositoryIdentifierEntity{Table: "safe WHERE 1=0; DROP TABLE users"}
		if _, err := repository.FindByIdContext(context.Background(), "1", entityType); err == nil {
			t.Fatal("strict read accepted unsafe table")
		}
		if state.countCalls("query") != 0 {
			t.Fatalf("unsafe strict read reached driver: %#v", state.snapshotCalls())
		}

		tm := NewTransactionManager(db)
		if err := tm.BeginContext(context.Background()); err != nil {
			t.Fatalf("begin: %v", err)
		}
		txRepository, err := tm.CrudRepository()
		if err != nil {
			t.Fatalf("transaction repository: %v", err)
		}
		if _, err := txRepository.DeleteByIdContext(context.Background(), "1", entityType); err == nil {
			t.Fatal("transaction delete accepted unsafe table")
		}
		if state.countCalls("exec") != 0 {
			t.Fatalf("unsafe transaction delete reached driver: %#v", state.snapshotCalls())
		}
		if err := tm.Rollback(); err != nil {
			t.Fatalf("rollback: %v", err)
		}
	})

	t.Run("valid identifiers retain stable SQL", func(t *testing.T) {
		state := newScriptedDBState(scriptedStep{
			kind:          "exec",
			queryContains: "INSERT INTO stable_identifier_entity (a,id,z)",
			result:        scriptedResult{rowsAffected: 1},
		})
		repository := NewBaseCrudRepository(newStrictTestDb(t, state))
		if err := repository.Save(&repositoryIdentifierEntity{ID: "1", A: "a", Z: "z"}); err != nil {
			t.Fatalf("save: %v", err)
		}
	})
}

func TestRepositoryTypedNilAndMixedAutoIncrementFailClosed(t *testing.T) {
	state := newScriptedDBState()
	repository := NewBaseCrudRepository(newStrictTestDb(t, state))
	var typedNil *repositoryIdentifierEntity
	if err := repository.Save(typedNil); err == nil {
		t.Fatal("typed nil save was accepted")
	}
	if err := repository.SaveBatch([]IDbEntity{typedNil}); err == nil {
		t.Fatal("typed nil batch was accepted")
	}
	if _, err := GetEntityMetadataCacheInstance().GetOrBuild(typedNil); err == nil {
		t.Fatal("typed nil metadata was accepted")
	}
	if got := ResolveEntityTableName(typedNil); got != "" {
		t.Fatalf("typed nil table=%q, want empty", got)
	}
	if got := GetCrudManagerInstance().GetPrimaryKeyValue(typedNil); got != nil {
		t.Fatalf("typed nil primary key=%v, want nil", got)
	}

	first := &repositoryAutoEntity{Name: "generated"}
	second := &repositoryAutoEntity{ID: 7, Name: "explicit"}
	if err := repository.SaveBatchUpsert([]IDbEntity{first, second}); err == nil {
		t.Fatal("mixed zero and explicit auto IDs were accepted")
	}
	if state.countCalls("exec") != 0 {
		t.Fatalf("invalid auto batch reached driver: %#v", state.snapshotCalls())
	}

	if err := repository.SaveBatchUpsert([]IDbEntity{
		&repositoryIdentifierEntity{ID: "1"},
		&repositoryDifferentShapeEntity{ID: "2"},
	}); err == nil {
		t.Fatal("same-table entities with different column shapes were accepted")
	}
	if state.countCalls("exec") != 0 {
		t.Fatalf("mixed entity shapes reached driver: %#v", state.snapshotCalls())
	}
}

func TestRepositoryFieldMappingFailsClosed(t *testing.T) {
	t.Run("complex JSON failure does not silently drop column", func(t *testing.T) {
		state := newScriptedDBState()
		repository := NewBaseCrudRepository(newStrictTestDb(t, state))
		err := repository.Save(&repositoryBadJSONEntity{
			ID:      "1",
			Payload: map[string]any{"unsupported": make(chan int)},
		})
		if err == nil || !strings.Contains(err.Error(), "复杂字段序列化失败") {
			t.Fatalf("serialization failure was lost: %v", err)
		}
		if state.countCalls("exec") != 0 {
			t.Fatalf("invalid entity reached driver: %#v", state.snapshotCalls())
		}
	})

	t.Run("nil anonymous pointer preserves static columns", func(t *testing.T) {
		state := newScriptedDBState(scriptedStep{
			kind: "exec",
			result: scriptedResult{
				lastInsertID: 42,
				rowsAffected: 1,
			},
		})
		repository := NewBaseCrudRepository(newStrictTestDb(t, state))
		fields, err := repository.getFields(&repositoryNilEmbeddedEntity{})
		if err != nil {
			t.Fatalf("get fields: %v", err)
		}
		if _, exists := fields["id"]; !exists {
			t.Fatalf("nil embedded primary key column missing: %#v", fields)
		}
		if _, exists := fields["name"]; !exists {
			t.Fatalf("nil embedded data column missing: %#v", fields)
		}
		entity := &repositoryNilEmbeddedEntity{}
		if err := repository.Save(entity); err != nil {
			t.Fatalf("save nil embedded entity: %v", err)
		}
		if entity.RepositoryEmbeddedFields == nil || entity.ID != 42 {
			t.Fatalf("generated ID was not assigned through nil embedding: %#v", entity.RepositoryEmbeddedFields)
		}
	})

	t.Run("ambiguous and recursive metadata is rejected", func(t *testing.T) {
		state := newScriptedDBState()
		repository := NewBaseCrudRepository(newStrictTestDb(t, state))
		if err := repository.Save(&repositoryDuplicateColumnEntity{ID: "1"}); err == nil {
			t.Fatal("duplicate column mapping was accepted")
		}
		if err := repository.Save(&RepositoryRecursiveEntity{ID: "1"}); err == nil {
			t.Fatal("recursive anonymous embedding was accepted")
		}
		metadataCache := GetEntityMetadataCacheInstance()
		if _, err := metadataCache.GetOrBuild(&repositoryDuplicateColumnEntity{}); err == nil {
			t.Fatal("metadata cache accepted duplicate column mapping")
		}
		if _, err := metadataCache.GetOrBuild(&RepositoryRecursiveEntity{}); err == nil {
			t.Fatal("metadata cache accepted recursive anonymous embedding")
		}
		recursive := &RepositoryRecursiveEntity{ID: "cycle-id"}
		recursive.RepositoryRecursiveEntity = recursive
		cm := GetCrudManagerInstance()
		if got := cm.GetPrimaryKeyColumnName(recursive); got != "id" {
			t.Fatalf("recursive primary-key column=%q, want id", got)
		}
		if got := cm.GetPrimaryKeyValue(recursive); got != "cycle-id" {
			t.Fatalf("recursive primary-key value=%v, want cycle-id", got)
		}
		columns := make([]string, 0, 1)
		cm.collectColumnsRecursive(reflect.TypeOf(*recursive), &columns)
		if len(columns) != 1 || columns[0] != "id" {
			t.Fatalf("recursive columns=%v, want [id]", columns)
		}
		if state.countCalls("exec") != 0 {
			t.Fatalf("invalid metadata reached driver: %#v", state.snapshotCalls())
		}
	})
}

func TestRepositoryNonTransactionAutoIncrementStep(t *testing.T) {
	for _, testCase := range []struct {
		name string
		save func(*BaseCrudRepository, []IDbEntity) error
	}{
		{name: "batch insert", save: func(repository *BaseCrudRepository, entities []IDbEntity) error {
			return repository.SaveBatch(entities)
		}},
		{name: "batch upsert", save: func(repository *BaseCrudRepository, entities []IDbEntity) error {
			return repository.SaveBatchUpsert(entities)
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			state := newScriptedDBState(
				scriptedStep{
					kind:          "query",
					queryContains: "@@SESSION.auto_increment_increment",
					columns:       []string{"auto_increment_increment"},
					rows:          [][]driver.Value{{int64(3)}},
				},
				scriptedStep{
					kind: "exec",
					result: scriptedResult{
						lastInsertID: 10,
						rowsAffected: 2,
					},
				},
			)
			first := &repositoryAutoEntity{Name: "first"}
			second := &repositoryAutoEntity{Name: "second"}
			if err := testCase.save(
				NewBaseCrudRepository(newStrictTestDb(t, state)),
				[]IDbEntity{first, second},
			); err != nil {
				t.Fatalf("save: %v", err)
			}
			if first.ID != 10 || second.ID != 13 {
				t.Fatalf("IDs=(%d,%d), want (10,13)", first.ID, second.ID)
			}
		})
	}
}

func TestRepositoryRecoveryErrorsPropagate(t *testing.T) {
	t.Run("fault tolerant persistence failure joins query failure", func(t *testing.T) {
		queryErr := errors.New("driver bad connection")
		state := newScriptedDBState(scriptedStep{kind: "exec", execErr: queryErr})
		db := newStrictTestDb(t, state)
		blockedPath := filepath.Join(t.TempDir(), "not-a-directory")
		if err := os.WriteFile(blockedPath, []byte("blocked"), 0o600); err != nil {
			t.Fatalf("create blocked path: %v", err)
		}
		ftm := NewFaultTolerantManager(db, nil)
		ftm.SetPersistPath(blockedPath)
		db.FaultTolerantMgr = ftm

		entity := &repositoryIdentifierEntity{ID: "1"}
		err := NewBaseCrudRepository(db).Save(entity)
		if !errors.Is(err, queryErr) {
			t.Fatalf("query error lost: %v", err)
		}
		var pathErr *os.PathError
		if !errors.As(err, &pathErr) {
			t.Fatalf("recovery persistence cause lost: %v", err)
		}
		if entity.SerializeCalls != 1 {
			t.Fatalf("failed write ran serialization hook %d times, want 1", entity.SerializeCalls)
		}
	})

	t.Run("WAL is the single durable recovery owner", func(t *testing.T) {
		queryErr := errors.New("driver bad connection")
		state := newScriptedDBState(scriptedStep{kind: "exec", execErr: queryErr})
		db := newStrictTestDb(t, state)
		if err := db.configureDatabaseGeneration("epoch"); err != nil {
			t.Fatal(err)
		}
		repository := NewBaseCrudRepository(db)
		journal := NewLocalWriteJournal(filepath.Join(t.TempDir(), "wal"), repository)
		t.Cleanup(func() { _ = journal.StopStrict() })
		if err := journal.ConfigureDatabaseGeneration("epoch"); err != nil {
			t.Fatal(err)
		}
		repository.SetWriteJournal(journal)

		manager := NewFaultTolerantManager(db, nil)
		if err := manager.SetPersistPathStrict(filepath.Join(t.TempDir(), "failed-ops")); err != nil {
			t.Fatal(err)
		}
		if err := manager.ConfigureDatabaseGeneration("epoch"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = manager.StopStrict() })
		db.FaultTolerantMgr = manager

		err := repository.SaveBatchUpsert([]IDbEntity{&repositoryIdentifierEntity{ID: "1", A: "a", Z: "z"}})
		if !errors.Is(err, queryErr) {
			t.Fatalf("query error lost: %v", err)
		}
		if count := manager.GetFailedOperationCount(); count != 0 {
			t.Fatalf("WAL-protected failure duplicated into FTM: count=%d", count)
		}
		if count, countErr := journal.PendingCount(); countErr != nil || count != 1 {
			t.Fatalf("WAL pending=(%d,%v), want (1,nil)", count, countErr)
		}
	})

	t.Run("WAL cleanup failure is returned after successful write", func(t *testing.T) {
		driverEntered := make(chan struct{}, 1)
		driverRelease := make(chan struct{})
		state := newScriptedDBState(scriptedStep{
			kind:          "exec",
			driverEntered: driverEntered,
			driverRelease: driverRelease,
			result:        scriptedResult{rowsAffected: 1},
		})
		db := newStrictTestDb(t, state)
		repository := NewBaseCrudRepository(db)
		journalPath := filepath.Join(t.TempDir(), "wal")
		journal := NewLocalWriteJournal(journalPath, repository)
		t.Cleanup(func() { _ = journal.StopStrict() })
		repository.SetWriteJournal(journal)

		entity := &repositoryIdentifierEntity{ID: "1", A: "a", Z: "z"}
		done := make(chan error, 1)
		go func() {
			done <- repository.SaveBatchUpsert([]IDbEntity{entity})
		}()
		select {
		case <-driverEntered:
		case <-time.After(2 * time.Second):
			t.Fatal("write did not reach driver")
		}
		// SQL 已在途、WAL append 已完成；停止 WAL 后清理步骤必须严格传播错误。
		if err := journal.StopStrict(); err != nil {
			t.Fatalf("stop WAL before cleanup: %v", err)
		}
		close(driverRelease)
		select {
		case err := <-done:
			if !errors.Is(err, ErrWriteJournalCleanup) {
				t.Fatalf("WAL cleanup error lost: %v", err)
			}
			if entity.SerializeCalls != 1 {
				t.Fatalf("WAL write ran serialization hook %d times, want 1", entity.SerializeCalls)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("write did not finish")
		}
	})
}

func TestRepositoryWriteGenerationLeaseCoversWALSQLAndCleanup(t *testing.T) {
	rowsAffectedEntered := make(chan struct{}, 1)
	releaseRowsAffected := make(chan struct{})
	rowsAffectedReleased := false
	defer func() {
		if !rowsAffectedReleased {
			close(releaseRowsAffected)
		}
	}()
	state := newScriptedDBState(scriptedStep{
		kind: "exec",
		result: repositoryBlockingRowsAffectedResult{
			entered: rowsAffectedEntered,
			release: releaseRowsAffected,
		},
	})
	db := newStrictTestDb(t, state)
	if err := db.configureDatabaseGeneration("old"); err != nil {
		t.Fatalf("configure db generation: %v", err)
	}
	repository := NewBaseCrudRepository(db)
	journal := NewLocalWriteJournal(filepath.Join(t.TempDir(), "wal"), repository)
	t.Cleanup(func() { _ = journal.StopStrict() })
	if err := journal.ConfigureDatabaseGeneration("old"); err != nil {
		t.Fatalf("configure WAL generation: %v", err)
	}
	repository.SetWriteJournal(journal)
	db.WriteJournal = journal

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- repository.SaveBatchUpsert([]IDbEntity{
			&repositoryIdentifierEntity{ID: "player-1", A: "a", Z: "z"},
		})
	}()
	select {
	case <-rowsAffectedEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("write did not reach post-SQL RowsAffected stage")
	}
	if pending, err := journal.PendingCount(); err != nil || pending != 1 {
		t.Fatalf("WAL pending before SQL completion=(%d,%v), want (1,nil)", pending, err)
	}

	type transitionResult struct {
		transition *DatabaseGenerationTransition
		err        error
	}
	transitionDone := make(chan transitionResult, 1)
	go func() {
		transition, err := db.BeginDatabaseGenerationTransition("new")
		transitionDone <- transitionResult{transition: transition, err: err}
	}()
	waitForDatabaseGenerationUnavailable(t, db)
	select {
	case result := <-transitionDone:
		if result.transition != nil {
			_ = result.transition.Abort()
		}
		t.Fatalf("generation transition crossed an unfinished WAL write: %v", result.err)
	default:
	}

	close(releaseRowsAffected)
	rowsAffectedReleased = true
	if err := <-writeDone; err != nil {
		t.Fatalf("WAL write: %v", err)
	}
	result := <-transitionDone
	if result.err != nil {
		t.Fatalf("begin transition: %v", result.err)
	}
	journal.journalMu.Lock()
	entries, readErr := journal.readAllEntriesLocked()
	journal.journalMu.Unlock()
	if readErr != nil || len(entries) != 0 {
		_ = result.transition.Abort()
		t.Fatalf("transition acquired before WAL cleanup: entries=%d err=%v", len(entries), readErr)
	}
	if err := result.transition.Abort(); err != nil {
		t.Fatalf("abort transition: %v", err)
	}
}

func TestRawWritesHoldGenerationLeaseAndPropagateFailures(t *testing.T) {
	t.Run("ExecuteUpdate blocks cleanup and rejects a new old-generation write", func(t *testing.T) {
		driverEntered := make(chan struct{}, 1)
		driverRelease := make(chan struct{})
		driverReleased := false
		defer func() {
			if !driverReleased {
				close(driverRelease)
			}
		}()
		state := newScriptedDBState(scriptedStep{
			kind:          "exec",
			queryContains: "UPDATE generation_guard",
			driverEntered: driverEntered,
			driverRelease: driverRelease,
			result:        driver.RowsAffected(1),
		})
		db := newStrictTestDb(t, state)
		if err := db.configureDatabaseGeneration("old"); err != nil {
			t.Fatalf("configure generation: %v", err)
		}
		writeDone := make(chan error, 1)
		go func() {
			_, err := db.ExecuteUpdate("UPDATE generation_guard SET value = 1")
			writeDone <- err
		}()
		select {
		case <-driverEntered:
		case <-time.After(2 * time.Second):
			t.Fatal("raw update did not reach driver")
		}

		type transitionResult struct {
			transition *DatabaseGenerationTransition
			err        error
		}
		transitionDone := make(chan transitionResult, 1)
		go func() {
			transition, err := db.BeginDatabaseGenerationTransition("new")
			transitionDone <- transitionResult{transition: transition, err: err}
		}()
		waitForDatabaseGenerationUnavailable(t, db)
		if _, err := db.ExecuteUpdate("UPDATE must_not_reach_driver SET value = 2"); !errors.Is(err, ErrDatabaseGenerationBlocked) {
			t.Fatalf("new write during transition error=%v, want generation blocked", err)
		}
		select {
		case result := <-transitionDone:
			if result.transition != nil {
				_ = result.transition.Abort()
			}
			t.Fatalf("transition crossed in-flight raw write: %v", result.err)
		default:
		}
		close(driverRelease)
		driverReleased = true
		if err := <-writeDone; err != nil {
			t.Fatalf("raw update: %v", err)
		}
		result := <-transitionDone
		if result.err != nil {
			t.Fatalf("begin transition: %v", result.err)
		}
		if err := result.transition.Abort(); err != nil {
			t.Fatalf("abort transition: %v", err)
		}
		if calls := state.countCalls("exec"); calls != 1 {
			t.Fatalf("blocked write reached driver: calls=%d", calls)
		}
	})

	t.Run("RowsAffected error and panic keep their causes", func(t *testing.T) {
		rowsErr := errors.New("rows affected unavailable")
		state := newScriptedDBState(scriptedStep{
			kind:   "exec",
			result: scriptedResult{rowsAffectedErr: rowsErr},
		})
		if _, err := newStrictTestDb(t, state).ExecuteUpdate("UPDATE rows_error SET value = 1"); !errors.Is(err, rowsErr) {
			t.Fatalf("RowsAffected cause lost: %v", err)
		}

		panicCause := errors.New("driver result panic")
		panicState := newScriptedDBState(scriptedStep{
			kind:   "exec",
			result: repositoryPanicRowsAffectedResult{cause: panicCause},
		})
		if _, err := newStrictTestDb(t, panicState).ExecuteUpdate("UPDATE panic_result SET value = 1"); !errors.Is(err, panicCause) {
			t.Fatalf("panic cause lost: %v", err)
		}
	})

	t.Run("ExecuteWithConnection holds the lease through callback completion", func(t *testing.T) {
		db := newStrictTestDb(t, newScriptedDBState())
		if err := db.configureDatabaseGeneration("old"); err != nil {
			t.Fatalf("configure generation: %v", err)
		}
		callbackEntered := make(chan struct{}, 1)
		callbackRelease := make(chan struct{})
		callbackReleased := false
		defer func() {
			if !callbackReleased {
				close(callbackRelease)
			}
		}()
		callbackDone := make(chan error, 1)
		go func() {
			callbackDone <- db.ExecuteWithConnection(func(*sql.Conn) error {
				callbackEntered <- struct{}{}
				<-callbackRelease
				return nil
			})
		}()
		select {
		case <-callbackEntered:
		case <-time.After(2 * time.Second):
			t.Fatal("connection callback did not start")
		}

		type transitionResult struct {
			transition *DatabaseGenerationTransition
			err        error
		}
		transitionDone := make(chan transitionResult, 1)
		go func() {
			transition, err := db.BeginDatabaseGenerationTransition("new")
			transitionDone <- transitionResult{transition: transition, err: err}
		}()
		waitForDatabaseGenerationUnavailable(t, db)
		select {
		case result := <-transitionDone:
			if result.transition != nil {
				_ = result.transition.Abort()
			}
			t.Fatalf("transition crossed active connection callback: %v", result.err)
		default:
		}
		close(callbackRelease)
		callbackReleased = true
		if err := <-callbackDone; err != nil {
			t.Fatalf("connection callback: %v", err)
		}
		result := <-transitionDone
		if result.err != nil {
			t.Fatalf("begin transition: %v", result.err)
		}
		if err := result.transition.Abort(); err != nil {
			t.Fatalf("abort transition: %v", err)
		}
	})

	t.Run("legacy multi-row API stops after first failure", func(t *testing.T) {
		state := newScriptedDBState(
			scriptedStep{kind: "exec", execErr: errors.New("first row failed")},
			scriptedStep{kind: "exec", result: driver.RowsAffected(1)},
		)
		db := newStrictTestDb(t, state)
		if affected := db.ExecuteUpdateMultiRows("UPDATE legacy_batch SET value = ?", [][]any{{1}, {2}}); affected != 0 {
			t.Fatalf("affected=%d, want 0", affected)
		}
		if calls := state.countCalls("exec"); calls != 1 {
			t.Fatalf("legacy API continued after failure: calls=%d", calls)
		}
		if affected := db.ExecuteUpdateByStatement(nil); affected != 0 {
			t.Fatalf("nil statement affected=%d, want 0", affected)
		}
	})
}

func waitForDatabaseGenerationUnavailable(t *testing.T, db *Db) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !db.isDatabaseGenerationUnavailable() {
		if time.Now().After(deadline) {
			t.Fatal("generation transition did not publish unavailable state")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDBStmtContextCausePropagation(t *testing.T) {
	driverEntered := make(chan struct{}, 1)
	neverRelease := make(chan struct{})
	state := newScriptedDBState(scriptedStep{
		kind:           "exec",
		queryContains:  "UPDATE context_cause",
		driverEntered:  driverEntered,
		driverRelease:  neverRelease,
		respectContext: true,
	})
	db := newStrictTestDb(t, state)
	cause := errors.New("caller stopped native exec")
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := db.execContext(ctx, "UPDATE context_cause SET value = 1")
		done <- err
	}()
	select {
	case <-driverEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("exec did not reach driver")
	}
	cancel(cause)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
			t.Fatalf("context cause lost: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("context cancellation did not stop exec")
	}
}

func TestSQLStatementConstructorsSnapshotSQLList(t *testing.T) {
	input := []string{"SELECT 1", "SELECT 2"}
	query := NewQueryStatements(input, struct{}{})
	update := NewUpdateStatements(input)
	input[0] = "DROP TABLE users"
	if query.SqlList[0] != "SELECT 1" || update.SqlList[0] != "SELECT 1" {
		t.Fatalf("constructors retained caller-owned SQL slice: query=%v update=%v", query.SqlList, update.SqlList)
	}
}

func TestMySQLStrategyIdentifierEscapingAndRowsError(t *testing.T) {
	strategy := NewMySQLStrategy(GetCrudManagerInstance())
	sqlText, err := strategy.GenerateDropColumnSQL("safe`; DROP TABLE victims; --", "bad`column")
	if err != nil {
		t.Fatalf("generate drop column: %v", err)
	}
	if !strings.Contains(sqlText, "`safe``; DROP TABLE victims; --`") || !strings.Contains(sqlText, "`bad``column`") {
		t.Fatalf("identifiers were not escaped: %s", sqlText)
	}

	rowErr := errors.New("column iteration failed")
	state := newScriptedDBState(scriptedStep{
		kind:          "query",
		queryContains: "information_schema.COLUMNS",
		columns:       []string{"COLUMN_NAME"},
		rows:          [][]driver.Value{{"id"}},
		rowErrAt:      1,
		rowErr:        rowErr,
	})
	if _, err := strategy.GetExistingColumns(newStrictTestDb(t, state), "users"); !errors.Is(err, rowErr) {
		t.Fatalf("rows error lost: %v", err)
	}
}
