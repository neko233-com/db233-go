package db233

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTransactionSavepointSafety(t *testing.T) {
	state := newScriptedDBState(
		scriptedStep{kind: "exec", queryContains: "SAVEPOINT db233_sp_foo"},
		scriptedStep{kind: "exec", queryContains: "ROLLBACK TO SAVEPOINT db233_sp_foo"},
		scriptedStep{kind: "exec", queryContains: "RELEASE SAVEPOINT db233_sp_foo"},
	)
	tm := NewTransactionManager(newStrictTestDb(t, state))
	if err := tm.BeginContext(context.Background()); err != nil {
		t.Fatalf("begin: %v", err)
	}

	for _, invalidName := range []string{
		"",
		"1starts_with_digit",
		"contains-dash",
		"sp; rollback",
		strings.Repeat("a", maxTransactionSavepointNameBytes+1),
	} {
		if err := tm.Savepoint(invalidName); err == nil {
			t.Fatalf("unsafe savepoint name accepted: %q", invalidName)
		}
	}
	if state.countCalls("exec") != 0 {
		t.Fatalf("unsafe savepoint reached driver: %#v", state.snapshotCalls())
	}

	if err := tm.Savepoint("Foo"); err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if got := tm.GetSavepoints(); len(got) != 1 || got[0] != "foo" {
		t.Fatalf("canonical savepoints=%v, want [foo]", got)
	}
	if err := tm.Savepoint("fOO"); err == nil {
		t.Fatal("case-equivalent duplicate savepoint was accepted")
	}
	if state.countCalls("exec") != 1 {
		t.Fatalf("duplicate savepoint reached driver: %#v", state.snapshotCalls())
	}
	if err := tm.RollbackToSavepoint("FOO"); err != nil {
		t.Fatalf("case-insensitive rollback: %v", err)
	}
	if err := tm.ReleaseSavepoint("foo"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := tm.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
}

func TestTransactionGenerationLeaseCoversEntireLifecycle(t *testing.T) {
	db := newStrictTestDb(t, newScriptedDBState())
	if err := db.configureDatabaseGeneration("old"); err != nil {
		t.Fatalf("configure generation: %v", err)
	}
	tm := NewTransactionManager(db)
	if err := tm.BeginContext(context.Background()); err != nil {
		t.Fatalf("begin: %v", err)
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
		t.Fatalf("generation transition crossed active transaction: %v", result.err)
	default:
	}

	if err := tm.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	result := <-transitionDone
	if result.err != nil {
		t.Fatalf("begin transition after rollback: %v", result.err)
	}
	if err := result.transition.Abort(); err != nil {
		t.Fatalf("abort transition: %v", err)
	}
}

func TestCanceledTransactionReleasesGenerationLease(t *testing.T) {
	db := newStrictTestDb(t, newScriptedDBState())
	if err := db.configureDatabaseGeneration("old"); err != nil {
		t.Fatalf("configure generation: %v", err)
	}
	tm := NewTransactionManager(db)
	if err := tm.BeginContext(context.Background(), TransactionOptions{Timeout: 20 * time.Millisecond}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for tm.IsActive() {
		if time.Now().After(deadline) {
			t.Fatal("canceled transaction remained active and retained generation lease")
		}
		time.Sleep(time.Millisecond)
	}

	transitionDone := make(chan struct {
		transition *DatabaseGenerationTransition
		err        error
	}, 1)
	go func() {
		transition, err := db.BeginDatabaseGenerationTransition("new")
		transitionDone <- struct {
			transition *DatabaseGenerationTransition
			err        error
		}{transition: transition, err: err}
	}()
	select {
	case result := <-transitionDone:
		if result.err != nil {
			t.Fatalf("transition after automatic rollback: %v", result.err)
		}
		if err := result.transition.Abort(); err != nil {
			t.Fatalf("abort transition: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("automatic rollback did not release generation lease")
	}
}

func TestTransactionSavepointContextCause(t *testing.T) {
	state := newScriptedDBState()
	tm := NewTransactionManager(newStrictTestDb(t, state))
	if err := tm.BeginContext(context.Background()); err != nil {
		t.Fatalf("begin: %v", err)
	}
	cause := errors.New("savepoint canceled by caller")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if err := tm.SavepointContext(ctx, "safe"); !errors.Is(err, cause) || !errors.Is(err, context.Canceled) {
		t.Fatalf("savepoint context cause lost: %v", err)
	}
	if state.countCalls("exec") != 0 {
		t.Fatalf("canceled savepoint reached driver: %#v", state.snapshotCalls())
	}
	if err := tm.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
}

func TestTransactionPostgreSQLReleaseDropsLaterSavepoints(t *testing.T) {
	state := newScriptedDBState(
		scriptedStep{kind: "exec", queryContains: "SAVEPOINT db233_sp_first"},
		scriptedStep{kind: "exec", queryContains: "SAVEPOINT db233_sp_second"},
		scriptedStep{kind: "exec", queryContains: "SAVEPOINT db233_sp_third"},
		scriptedStep{kind: "exec", queryContains: "RELEASE SAVEPOINT db233_sp_second"},
	)
	db := newStrictTestDb(t, state)
	db.DatabaseType = EnumDatabaseTypePostgreSQL
	tm := NewTransactionManager(db)
	if err := tm.BeginContext(context.Background()); err != nil {
		t.Fatalf("begin: %v", err)
	}
	for _, name := range []string{"first", "second", "third"} {
		if err := tm.Savepoint(name); err != nil {
			t.Fatalf("savepoint %s: %v", name, err)
		}
	}
	if err := tm.ReleaseSavepoint("second"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := tm.GetSavepoints(); len(got) != 1 || got[0] != "first" {
		t.Fatalf("savepoints=%v, want [first]", got)
	}
	if err := tm.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
}

func TestTransactionOperationUsesTransactionContextCause(t *testing.T) {
	driverEntered := make(chan struct{}, 1)
	neverRelease := make(chan struct{})
	state := newScriptedDBState(scriptedStep{
		kind:           "exec",
		queryContains:  "UPDATE context_guard",
		driverEntered:  driverEntered,
		driverRelease:  neverRelease,
		respectContext: true,
	})
	tm := NewTransactionManager(newStrictTestDb(t, state))
	txCause := errors.New("transaction owner stopped")
	txCtx, cancel := context.WithCancelCause(context.Background())
	if err := tm.BeginContext(txCtx); err != nil {
		t.Fatalf("begin: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := tm.Exec("UPDATE context_guard SET value = 1")
		done <- err
	}()
	select {
	case <-driverEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("exec did not reach driver")
	}
	cancel(txCause)
	select {
	case err := <-done:
		if !errors.Is(err, txCause) || !errors.Is(err, context.Canceled) {
			t.Fatalf("transaction context cause lost: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("transaction context did not cancel in-flight Exec")
	}

	waitForScriptedCalls(t, state, "rollback", 1)
	_ = tm.Commit() // 仅重置 manager；底层事务已由 database/sql 回滚。
}

func TestTransactionRepositoryUsesTransactionDeadline(t *testing.T) {
	neverRelease := make(chan struct{})
	state := newScriptedDBState(scriptedStep{
		kind:           "exec",
		queryContains:  "INSERT",
		driverRelease:  neverRelease,
		respectContext: true,
	})
	tm := NewTransactionManager(newStrictTestDb(t, state))
	if err := tm.BeginContext(context.Background(), TransactionOptions{Timeout: 20 * time.Millisecond}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	repository, err := tm.CrudRepository()
	if err != nil {
		t.Fatalf("repository: %v", err)
	}
	start := time.Now()
	err = repository.SaveContext(context.Background(), &serialContractEntity{ID: "deadline"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("transaction deadline cause lost: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("transaction deadline did not bound repository write: %v", elapsed)
	}
	waitForScriptedCalls(t, state, "rollback", 1)
	_ = tm.Commit()
}

func TestTransactionAutoIncrementStepAndDuplicateGuards(t *testing.T) {
	t.Run("session increment controls ID backfill", func(t *testing.T) {
		state := newScriptedDBState(
			scriptedStep{
				kind:    "query",
				columns: []string{"@@SESSION.auto_increment_increment"},
				rows:    [][]driver.Value{{int64(2)}},
			},
			scriptedStep{
				kind:   "exec",
				result: scriptedResult{lastInsertID: 100, rowsAffected: 2},
			},
		)
		tm := NewTransactionManager(newStrictTestDb(t, state))
		if err := tm.BeginContext(context.Background()); err != nil {
			t.Fatalf("begin: %v", err)
		}
		repository, err := tm.CrudRepository()
		if err != nil {
			t.Fatalf("repository: %v", err)
		}
		first := &strictAutoIDEntity{Name: "first"}
		second := &strictAutoIDEntity{Name: "second"}
		if err := repository.SaveBatchUpsertContext(context.Background(), []IDbEntity{first, second}); err != nil {
			t.Fatalf("batch: %v", err)
		}
		if err := tm.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
		if first.ID != 100 || second.ID != 102 {
			t.Fatalf("IDs=(%d,%d), want (100,102)", first.ID, second.ID)
		}
	})

	t.Run("duplicate instance in one batch fails before hooks and SQL", func(t *testing.T) {
		state := newScriptedDBState()
		tm := NewTransactionManager(newStrictTestDb(t, state))
		if err := tm.BeginContext(context.Background()); err != nil {
			t.Fatalf("begin: %v", err)
		}
		repository, err := tm.CrudRepository()
		if err != nil {
			t.Fatalf("repository: %v", err)
		}
		entity := &strictAutoIDEntity{Name: "duplicate"}
		if err := repository.SaveBatchUpsertContext(context.Background(), []IDbEntity{entity, entity}); err == nil {
			t.Fatal("duplicate entity instance accepted")
		}
		if entity.SerializeCalls != 0 || state.countCalls("exec") != 0 {
			t.Fatalf("duplicate reached hook/driver: hooks=%d calls=%#v", entity.SerializeCalls, state.snapshotCalls())
		}
		if err := tm.Rollback(); err != nil {
			t.Fatalf("rollback: %v", err)
		}
	})

	t.Run("same pending auto entity cannot be inserted twice", func(t *testing.T) {
		state := newScriptedDBState(scriptedStep{
			kind:   "exec",
			result: scriptedResult{lastInsertID: 500, rowsAffected: 1},
		})
		tm := NewTransactionManager(newStrictTestDb(t, state))
		if err := tm.BeginContext(context.Background()); err != nil {
			t.Fatalf("begin: %v", err)
		}
		repository, err := tm.CrudRepository()
		if err != nil {
			t.Fatalf("repository: %v", err)
		}
		entity := &strictAutoIDEntity{Name: "repeat"}
		if err := repository.SaveContext(context.Background(), entity); err != nil {
			t.Fatalf("first save: %v", err)
		}
		if err := repository.SaveContext(context.Background(), entity); err == nil {
			t.Fatal("same pending entity was inserted twice")
		}
		if state.countCalls("exec") != 1 {
			t.Fatalf("repeat save reached driver: %#v", state.snapshotCalls())
		}
		if entity.SerializeCalls != 1 {
			t.Fatalf("repeat save reached serialization hook: calls=%d", entity.SerializeCalls)
		}
		if err := tm.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
		if entity.ID != 500 {
			t.Fatalf("ID=%d, want 500", entity.ID)
		}
	})

	t.Run("rollback to savepoint releases pending auto entity", func(t *testing.T) {
		state := newScriptedDBState(
			scriptedStep{kind: "exec", queryContains: "SAVEPOINT db233_sp_before_insert"},
			scriptedStep{kind: "exec", result: scriptedResult{lastInsertID: 10, rowsAffected: 1}},
			scriptedStep{kind: "exec", queryContains: "ROLLBACK TO SAVEPOINT db233_sp_before_insert"},
			scriptedStep{kind: "exec", result: scriptedResult{lastInsertID: 20, rowsAffected: 1}},
		)
		tm := NewTransactionManager(newStrictTestDb(t, state))
		if err := tm.BeginContext(context.Background()); err != nil {
			t.Fatalf("begin: %v", err)
		}
		if err := tm.Savepoint("before_insert"); err != nil {
			t.Fatalf("savepoint: %v", err)
		}
		repository, err := tm.CrudRepository()
		if err != nil {
			t.Fatalf("repository: %v", err)
		}
		entity := &strictAutoIDEntity{Name: "retry"}
		if err := repository.SaveContext(context.Background(), entity); err != nil {
			t.Fatalf("first save: %v", err)
		}
		if err := tm.RollbackToSavepoint("before_insert"); err != nil {
			t.Fatalf("rollback to savepoint: %v", err)
		}
		if err := repository.SaveContext(context.Background(), entity); err != nil {
			t.Fatalf("save after rollback: %v", err)
		}
		if err := tm.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
		if entity.ID != 20 {
			t.Fatalf("ID=%d, want 20", entity.ID)
		}
	})
}

func TestMigrationMySQLImplicitCommitAndDownOrder(t *testing.T) {
	t.Run("metadata failure is explicit and never claims rollback", func(t *testing.T) {
		recordErr := errors.New("migration record failed")
		state := newScriptedDBState(
			scriptedStep{kind: "exec", queryContains: "CREATE TABLE migration_guard"},
			scriptedStep{kind: "exec", queryContains: "INSERT INTO schema_migrations", execErr: recordErr},
		)
		manager := NewMigrationManager(newStrictTestDb(t, state), "")
		err := manager.applyMigration(Migration{
			Version: 10,
			Name:    "implicit_commit",
			UpSQL:   "CREATE TABLE migration_guard (id BIGINT)",
		}, true)
		if !errors.Is(err, ErrMigrationMetadataOutOfSync) || !errors.Is(err, recordErr) {
			t.Fatalf("migration state error lost: %v", err)
		}
		if state.countCalls("begin") != 0 || state.countCalls("commit") != 0 || state.countCalls("rollback") != 0 {
			t.Fatalf("MySQL DDL was wrapped in fake transaction: %#v", state.snapshotCalls())
		}
	})

	t.Run("metadata zero rows is explicit state mismatch", func(t *testing.T) {
		state := newScriptedDBState(
			scriptedStep{kind: "exec", queryContains: "DROP TABLE migration_guard"},
			scriptedStep{
				kind:          "exec",
				queryContains: "DELETE FROM schema_migrations",
				result:        scriptedResult{rowsAffected: 0},
			},
		)
		manager := NewMigrationManager(newStrictTestDb(t, state), "")
		err := manager.applyMigration(Migration{
			Version: 10,
			Name:    "implicit_commit",
			DownSQL: "DROP TABLE migration_guard",
		}, false)
		if !errors.Is(err, ErrMigrationMetadataOutOfSync) {
			t.Fatalf("migration state mismatch lost: %v", err)
		}
	})

	t.Run("Down hydrates SQL and runs newest first without duplication", func(t *testing.T) {
		dir := t.TempDir()
		for version := 1; version <= 3; version++ {
			upPath := filepath.Join(dir, fmt.Sprintf("%d_test.up.sql", version))
			downPath := filepath.Join(dir, fmt.Sprintf("%d_test.down.sql", version))
			if err := os.WriteFile(upPath, []byte(fmt.Sprintf("CREATE TABLE up_migration_%d (id BIGINT)", version)), 0o600); err != nil {
				t.Fatalf("write up migration: %v", err)
			}
			if err := os.WriteFile(downPath, []byte(fmt.Sprintf("DROP TABLE down_migration_%d", version)), 0o600); err != nil {
				t.Fatalf("write down migration: %v", err)
			}
		}

		now := time.Now()
		state := newScriptedDBState(
			scriptedStep{
				kind:    "query",
				columns: []string{"version", "name", "applied_at"},
				rows: [][]driver.Value{
					{int64(1), "test", now},
					{int64(2), "test", now},
					{int64(3), "test", now},
				},
			},
			scriptedStep{kind: "exec", queryContains: "DROP TABLE down_migration_3"},
			scriptedStep{kind: "exec", queryContains: "DELETE FROM schema_migrations", result: scriptedResult{rowsAffected: 1}},
			scriptedStep{kind: "exec", queryContains: "DROP TABLE down_migration_2"},
			scriptedStep{kind: "exec", queryContains: "DELETE FROM schema_migrations", result: scriptedResult{rowsAffected: 1}},
			scriptedStep{kind: "exec", queryContains: "DROP TABLE down_migration_1"},
			scriptedStep{kind: "exec", queryContains: "DELETE FROM schema_migrations", result: scriptedResult{rowsAffected: 1}},
		)
		manager := NewMigrationManager(newStrictTestDb(t, state), dir)
		if err := manager.Down(0); err != nil {
			t.Fatalf("down: %v", err)
		}
	})
}

func TestMySQLMigrationImplicitCommitDetection(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want bool
	}{
		{name: "plain dml", sql: "INSERT INTO audit_log(message) VALUES ('DROP TABLE hidden;')", want: false},
		{name: "commented ddl", sql: "-- DROP TABLE ignored\nUPDATE audit_log SET message = 'ALTER TABLE ignored'", want: false},
		{name: "later ddl", sql: "UPDATE audit_log SET message = 'ok'; /* safe */ ALTER TABLE users ADD active BOOL", want: true},
		{name: "utf8 bom", sql: "\ufeffCREATE TABLE bom_guard (id BIGINT)", want: true},
		{name: "administrative implicit commit", sql: "ANALYZE TABLE users", want: true},
		{name: "version comment", sql: "/*!80000 CREATE TABLE version_guard (id BIGINT) */", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := mysqlMigrationHasImplicitCommit(test.sql); got != test.want {
				t.Fatalf("mysqlMigrationHasImplicitCommit()=%v, want %v", got, test.want)
			}
		})
	}
}

func TestTransactionExecRejectsMySQLImplicitCommitBeforeDriver(t *testing.T) {
	state := newScriptedDBState()
	tm := NewTransactionManager(newStrictTestDb(t, state))
	if err := tm.Begin(); err != nil {
		t.Fatal(err)
	}
	if _, err := tm.Exec("ALTER TABLE users ADD COLUMN unsafe INT"); err == nil {
		t.Fatal("transaction accepted MySQL implicit-commit DDL")
	}
	if state.countCalls("exec") != 0 {
		t.Fatalf("implicit-commit DDL reached driver: %+v", state.snapshotCalls())
	}
	if err := tm.Rollback(); err != nil {
		t.Fatal(err)
	}
}
