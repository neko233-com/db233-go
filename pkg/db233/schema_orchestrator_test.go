package db233

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

type schemaOrchestratorEntity struct {
	ID   string `db:"id" primary_key:"true"`
	Name string `db:"name,not_null" size:"128"`
}

func (*schemaOrchestratorEntity) TableName() string       { return "schema_orchestrator_entities" }
func (*schemaOrchestratorEntity) SerializeBeforeSaveDb()  {}
func (*schemaOrchestratorEntity) DeserializeAfterLoadDb() {}
func (*schemaOrchestratorEntity) GetTableMetaData() *TableMetaData {
	return &TableMetaData{
		TableName: "schema_orchestrator_entities",
		Indexes: []*IndexMetaData{{
			IndexName: "idx_schema_name",
			Columns:   []string{"name"},
		}},
	}
}

type schemaOrchestratorConflictEntity struct {
	ID int64 `db:"id" primary_key:"true"`
}

func (*schemaOrchestratorConflictEntity) TableName() string {
	return "schema_orchestrator_entities"
}
func (*schemaOrchestratorConflictEntity) SerializeBeforeSaveDb()  {}
func (*schemaOrchestratorConflictEntity) DeserializeAfterLoadDb() {}

type schemaOrchestratorRaceEntity struct {
	ID string `db:"id" primary_key:"true"`
}

func (*schemaOrchestratorRaceEntity) TableName() string       { return "schema_orchestrator_race" }
func (*schemaOrchestratorRaceEntity) SerializeBeforeSaveDb()  {}
func (*schemaOrchestratorRaceEntity) DeserializeAfterLoadDb() {}

type schemaOrchestratorUnsafeTypeEntity struct {
	ID      string `db:"id" primary_key:"true"`
	Payload string `db:"payload" db_type:"VARCHAR(255); DROP TABLE users"`
}

func (*schemaOrchestratorUnsafeTypeEntity) TableName() string       { return "schema_unsafe_type" }
func (*schemaOrchestratorUnsafeTypeEntity) SerializeBeforeSaveDb()  {}
func (*schemaOrchestratorUnsafeTypeEntity) DeserializeAfterLoadDb() {}

type schemaOrchestratorUnsafeTableEntity struct {
	ID string `db:"id" primary_key:"true"`
}

func (*schemaOrchestratorUnsafeTableEntity) TableName() string       { return "safe; DROP TABLE users" }
func (*schemaOrchestratorUnsafeTableEntity) SerializeBeforeSaveDb()  {}
func (*schemaOrchestratorUnsafeTableEntity) DeserializeAfterLoadDb() {}

type schemaOrchestratorPanicTableEntity struct {
	ID string `db:"id" primary_key:"true"`
}

func (*schemaOrchestratorPanicTableEntity) TableName() string {
	panic("private table panic value")
}
func (*schemaOrchestratorPanicTableEntity) SerializeBeforeSaveDb()  {}
func (*schemaOrchestratorPanicTableEntity) DeserializeAfterLoadDb() {}

type schemaOrchestratorPanicMetadataEntity struct {
	ID string `db:"id" primary_key:"true"`
}

func (*schemaOrchestratorPanicMetadataEntity) TableName() string       { return "schema_panic_metadata" }
func (*schemaOrchestratorPanicMetadataEntity) SerializeBeforeSaveDb()  {}
func (*schemaOrchestratorPanicMetadataEntity) DeserializeAfterLoadDb() {}
func (*schemaOrchestratorPanicMetadataEntity) GetTableMetaData() *TableMetaData {
	panic("private metadata panic value")
}

type schemaOrchestratorAutoIncrementEntity struct {
	ID int64 `db:"id" primary_key:"true" auto_increment:"true"`
}

func (*schemaOrchestratorAutoIncrementEntity) TableName() string       { return "schema_auto_increment" }
func (*schemaOrchestratorAutoIncrementEntity) SerializeBeforeSaveDb()  {}
func (*schemaOrchestratorAutoIncrementEntity) DeserializeAfterLoadDb() {}

func schemaTableExistsStep(exists bool) scriptedStep {
	count := int64(0)
	if exists {
		count = 1
	}
	return scriptedStep{
		kind:          "query",
		queryContains: "information_schema.TABLES",
		columns:       []string{"COUNT(*)"},
		rows:          [][]driver.Value{{count}},
	}
}

func schemaColumnsStep(rows ...[]driver.Value) scriptedStep {
	return scriptedStep{
		kind:          "query",
		queryContains: "information_schema.COLUMNS",
		columns:       []string{"COLUMN_NAME", "COLUMN_TYPE", "IS_NULLABLE", "COLUMN_KEY", "COLUMN_DEFAULT", "EXTRA"},
		rows:          rows,
	}
}

func schemaIndexesStep(rows ...[]driver.Value) scriptedStep {
	return scriptedStep{
		kind:          "query",
		queryContains: "information_schema.STATISTICS",
		columns:       []string{"INDEX_NAME", "COLUMN_NAME", "NON_UNIQUE"},
		rows:          rows,
	}
}

func schemaEntityColumns() [][]driver.Value {
	return [][]driver.Value{
		{"id", "varchar(255)", "NO", "PRI", nil, ""},
		{"name", "varchar(128)", "NO", "", nil, ""},
	}
}

func schemaEntityIndexRows() [][]driver.Value {
	return [][]driver.Value{{"idx_schema_name", "name", int64(1)}}
}

func TestSchemaOrchestratorDryRunPlansSafeChanges(t *testing.T) {
	state := newScriptedDBState(
		schemaTableExistsStep(true),
		schemaColumnsStep([]driver.Value{"id", "varchar(255)", "NO", "PRI", nil, ""}),
		schemaIndexesStep(),
	)
	db := newStrictTestDb(t, state)
	report, err := db.AutoMigrateSchema(context.Background(), []any{&schemaOrchestratorEntity{}}, &SchemaMigrationOptions{
		MaxConcurrency: 1,
		DryRun:         true,
	})
	if err != nil {
		t.Fatalf("AutoMigrateSchema dry-run: %v", err)
	}
	if len(report.Tables) != 1 || len(report.Tables[0].Actions) != 2 {
		t.Fatalf("unexpected dry-run plan: %+v", report.Tables)
	}
	if report.Tables[0].Actions[0].Kind != SchemaActionCreateColumn ||
		report.Tables[0].Actions[1].Kind != SchemaActionCreateIndex {
		t.Fatalf("unexpected action order: %+v", report.Tables[0].Actions)
	}
	if report.Tables[0].Executed != 0 || state.countCalls("exec") != 0 {
		t.Fatalf("dry-run executed DDL: report=%+v calls=%+v", report.Tables[0], state.snapshotCalls())
	}
	if !reflect.DeepEqual(report.Before, report.After) {
		t.Fatalf("dry-run after must equal before: before=%+v after=%+v", report.Before, report.After)
	}
}

func TestSchemaOrchestratorExecutesAndFinallyVerifies(t *testing.T) {
	state := newScriptedDBState(
		schemaTableExistsStep(false),
		scriptedStep{kind: "exec", queryContains: "CREATE TABLE"},
		scriptedStep{kind: "exec", queryContains: "CREATE INDEX"},
		schemaTableExistsStep(true),
		schemaColumnsStep(schemaEntityColumns()...),
		schemaIndexesStep(schemaEntityIndexRows()...),
	)
	db := newStrictTestDb(t, state)
	report, err := db.AutoMigrateSchema(context.Background(), []any{&schemaOrchestratorEntity{}}, &SchemaMigrationOptions{
		MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatalf("AutoMigrateSchema: %v", err)
	}
	if report.Before.Compatible || !report.After.Compatible || !report.After.Exact {
		t.Fatalf("unexpected verification transition: before=%+v after=%+v", report.Before, report.After)
	}
	if len(report.Tables) != 1 || report.Tables[0].Executed != 2 {
		t.Fatalf("unexpected execution report: %+v", report.Tables)
	}
}

func TestSchemaOrchestratorAcceptsEquivalentConcurrentDDL(t *testing.T) {
	duplicateErr := errors.New("table already exists")
	columns := [][]driver.Value{{"id", "varchar(255)", "NO", "PRI", nil, ""}}
	state := newScriptedDBState(
		schemaTableExistsStep(false),
		scriptedStep{kind: "exec", queryContains: "CREATE TABLE", execErr: duplicateErr},
		schemaTableExistsStep(true),
		schemaColumnsStep(columns...),
		schemaIndexesStep(),
		schemaTableExistsStep(true),
		schemaColumnsStep(columns...),
		schemaIndexesStep(),
	)
	db := newStrictTestDb(t, state)
	report, err := db.AutoMigrateSchema(context.Background(), []any{&schemaOrchestratorRaceEntity{}}, &SchemaMigrationOptions{
		MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatalf("equivalent concurrent DDL must be idempotent: %v", err)
	}
	if len(report.Tables) != 1 || report.Tables[0].Executed != 1 || !report.After.Compatible {
		t.Fatalf("unexpected concurrent DDL report: %+v", report)
	}
}

func TestSchemaOrchestratorSafeDefaultsBlockDestructiveDrift(t *testing.T) {
	state := newScriptedDBState(
		schemaTableExistsStep(true),
		schemaColumnsStep(
			[]driver.Value{"id", "varchar(255)", "NO", "PRI", nil, ""},
			[]driver.Value{"name", "varchar(64)", "NO", "", nil, ""},
			[]driver.Value{"legacy", "int", "YES", "", nil, ""},
		),
		schemaIndexesStep(
			[]driver.Value{"idx_extra", "legacy", int64(1)},
			[]driver.Value{"idx_schema_name", "id", int64(1)},
		),
	)
	db := newStrictTestDb(t, state)
	report, err := db.AutoMigrateSchema(context.Background(), []any{&schemaOrchestratorEntity{}}, &SchemaMigrationOptions{
		MaxConcurrency: 1,
		DryRun:         true,
	})
	if err != nil {
		t.Fatalf("safe default dry-run: %v", err)
	}
	table := report.Tables[0]
	if len(table.Actions) != 0 || len(table.BlockedIssues) != 4 {
		t.Fatalf("destructive drift was not blocked: %+v", table)
	}
	for _, issue := range table.BlockedIssues {
		if issue.BlockedBy == "" {
			t.Fatalf("blocked issue missing permission: %+v", issue)
		}
	}
}

func TestVerifySchemaCompatibleAndExactModes(t *testing.T) {
	steps := make([]scriptedStep, 0, 6)
	for iteration := 0; iteration < 2; iteration++ {
		steps = append(steps,
			schemaTableExistsStep(true),
			schemaColumnsStep(
				[]driver.Value{"id", "varchar(255)", "NO", "PRI", nil, ""},
				[]driver.Value{"name", "varchar(128)", "NO", "", nil, ""},
				[]driver.Value{"legacy", "int", "YES", "", nil, ""},
			),
			schemaIndexesStep(
				[]driver.Value{"idx_extra", "legacy", int64(1)},
				[]driver.Value{"idx_schema_name", "name", int64(1)},
			),
		)
	}
	state := newScriptedDBState(steps...)
	db := newStrictTestDb(t, state)
	report, err := db.VerifySchema(context.Background(), []any{&schemaOrchestratorEntity{}}, &SchemaVerifyOptions{
		MaxConcurrency: 1,
	})
	if err != nil || !report.Compatible || report.Exact {
		t.Fatalf("compatible mode mismatch: report=%+v err=%v", report, err)
	}
	report, err = db.VerifySchema(context.Background(), []any{&schemaOrchestratorEntity{}}, &SchemaVerifyOptions{
		MaxConcurrency: 1,
		RequireExact:   true,
	})
	if !errors.Is(err, ErrSchemaVerificationFailed) || !report.Compatible || report.Exact {
		t.Fatalf("exact mode mismatch: report=%+v err=%v", report, err)
	}
}

func TestVerifySchemaIncompatibleReturnsSentinelAndReport(t *testing.T) {
	state := newScriptedDBState(schemaTableExistsStep(false))
	db := newStrictTestDb(t, state)
	report, err := db.VerifySchema(context.Background(), []any{&schemaOrchestratorRaceEntity{}}, &SchemaVerifyOptions{
		MaxConcurrency: 1,
	})
	if !errors.Is(err, ErrSchemaVerificationFailed) {
		t.Fatalf("expected ErrSchemaVerificationFailed, got %v", err)
	}
	if report.Compatible || report.Exact || len(report.Tables) != 1 || report.Tables[0].Exists {
		t.Fatalf("incompatible report was not retained: %+v", report)
	}
}

func TestSchemaOrchestratorAutoIncrementVerificationAndPlan(t *testing.T) {
	state := newScriptedDBState(
		schemaTableExistsStep(true),
		schemaColumnsStep([]driver.Value{"id", "bigint", "NO", "PRI", nil, "auto_increment"}),
		schemaIndexesStep(),
		schemaTableExistsStep(true),
		schemaColumnsStep([]driver.Value{"id", "bigint", "NO", "PRI", nil, ""}),
		schemaIndexesStep(),
	)
	db := newStrictTestDb(t, state)
	verified, err := db.VerifySchema(context.Background(), []any{&schemaOrchestratorAutoIncrementEntity{}}, &SchemaVerifyOptions{
		MaxConcurrency: 1,
	})
	if err != nil || !verified.Compatible {
		t.Fatalf("auto-increment schema should verify: report=%+v err=%v", verified, err)
	}
	permissions := DefaultSchemaMigrationPermissions()
	permissions.UpdateColumn = true
	report, err := db.AutoMigrateSchema(context.Background(), []any{&schemaOrchestratorAutoIncrementEntity{}}, &SchemaMigrationOptions{
		MaxConcurrency: 1,
		DryRun:         true,
		Permissions:    &permissions,
	})
	if err != nil {
		t.Fatalf("auto-increment dry-run: %v", err)
	}
	if len(report.Tables) != 1 || len(report.Tables[0].Actions) != 1 ||
		report.Tables[0].Actions[0].Kind != SchemaActionUpdateColumn {
		t.Fatalf("unexpected auto-increment plan: %+v", report.Tables)
	}
	statement := report.Tables[0].Actions[0].Statement
	if !strings.Contains(statement, "AUTO_INCREMENT") || !strings.Contains(statement, "NOT NULL") {
		t.Fatalf("modify DDL lost independent tags: %s", statement)
	}
}

func TestAutoMigrateSchemaReturnsSentinelWhenFinalSchemaIsIncompatible(t *testing.T) {
	steps := make([]scriptedStep, 0, 6)
	for iteration := 0; iteration < 2; iteration++ {
		steps = append(steps,
			schemaTableExistsStep(true),
			schemaColumnsStep([]driver.Value{"id", "bigint", "NO", "PRI", nil, ""}),
			schemaIndexesStep(),
		)
	}
	state := newScriptedDBState(steps...)
	db := newStrictTestDb(t, state)
	report, err := db.AutoMigrateSchema(context.Background(), []any{&schemaOrchestratorAutoIncrementEntity{}}, &SchemaMigrationOptions{
		MaxConcurrency: 1,
	})
	if !errors.Is(err, ErrSchemaVerificationFailed) || report.After.Compatible {
		t.Fatalf("incompatible final schema must fail: report=%+v err=%v", report, err)
	}
	if len(report.Tables) != 1 || len(report.Tables[0].BlockedIssues) != 1 ||
		report.Tables[0].BlockedIssues[0].Kind != SchemaIssueColumnAutoIncrement {
		t.Fatalf("auto-increment drift was not reported: %+v", report.Tables)
	}
}

func TestSchemaOrchestratorRejectsConflictsAndInjectionBeforeDatabase(t *testing.T) {
	tests := []struct {
		name     string
		entities []any
	}{
		{
			name:     "conflicting entity types",
			entities: []any{&schemaOrchestratorEntity{}, &schemaOrchestratorConflictEntity{}},
		},
		{
			name:     "unsafe SQL type",
			entities: []any{&schemaOrchestratorUnsafeTypeEntity{}},
		},
		{
			name:     "unsafe table identifier",
			entities: []any{&schemaOrchestratorUnsafeTableEntity{}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := newScriptedDBState()
			db := newStrictTestDb(t, state)
			_, err := db.AutoMigrateSchema(context.Background(), test.entities, &SchemaMigrationOptions{
				MaxConcurrency: 1,
				DryRun:         true,
			})
			if err == nil {
				t.Fatal("expected validation error")
			}
			if calls := state.snapshotCalls(); len(calls) != 0 {
				t.Fatalf("invalid metadata reached database: %+v", calls)
			}
		})
	}
}

func TestSchemaOrchestratorRecoversOnlyUserMetadataPanics(t *testing.T) {
	tests := []struct {
		name      string
		prototype any
		secret    string
	}{
		{name: "TableName", prototype: &schemaOrchestratorPanicTableEntity{}, secret: "private table panic value"},
		{name: "GetTableMetaData", prototype: &schemaOrchestratorPanicMetadataEntity{}, secret: "private metadata panic value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := newScriptedDBState()
			db := newStrictTestDb(t, state)
			_, err := db.VerifySchema(context.Background(), []any{test.prototype}, &SchemaVerifyOptions{MaxConcurrency: 1})
			if err == nil {
				t.Fatal("expected recovered metadata callback error")
			}
			if strings.Contains(err.Error(), test.secret) {
				t.Fatalf("panic value leaked through strict error: %v", err)
			}
			if calls := state.snapshotCalls(); len(calls) != 0 {
				t.Fatalf("panicking metadata reached database: %+v", calls)
			}
		})
	}
}

func TestSchemaOrchestratorPropagatesMetadataContextCancellation(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	state := newScriptedDBState(scriptedStep{
		kind:           "query",
		queryContains:  "information_schema.TABLES",
		driverEntered:  entered,
		driverRelease:  release,
		respectContext: true,
	})
	db := newStrictTestDb(t, state)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := db.VerifySchema(ctx, []any{&schemaOrchestratorRaceEntity{}}, &SchemaVerifyOptions{MaxConcurrency: 1})
		result <- err
	}()
	<-entered
	cancel()
	err := <-result
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("context cancellation was not propagated: %v", err)
	}
}

func TestSchemaOrchestratorLockWaitIsContextAware(t *testing.T) {
	state := newScriptedDBState()
	db := newStrictTestDb(t, state)
	release, err := acquireSchemaDatabaseLock(context.Background(), db)
	if err != nil {
		t.Fatalf("acquire test schema lock: %v", err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = db.VerifySchema(ctx, []any{&schemaOrchestratorRaceEntity{}}, &SchemaVerifyOptions{MaxConcurrency: 1})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("schema lock wait did not propagate deadline: %v", err)
	}
	if calls := state.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("lock waiter unexpectedly reached database: %+v", calls)
	}
}

func TestSchemaOrchestratorHoldsGenerationLeaseForWholeCall(t *testing.T) {
	entered := make(chan struct{}, 1)
	releaseQuery := make(chan struct{})
	state := newScriptedDBState(
		scriptedStep{
			kind:          "query",
			queryContains: "information_schema.TABLES",
			driverEntered: entered,
			driverRelease: releaseQuery,
			columns:       []string{"COUNT(*)"},
			rows:          [][]driver.Value{{int64(1)}},
		},
		schemaColumnsStep([]driver.Value{"id", "varchar(255)", "NO", "PRI", nil, ""}),
		schemaIndexesStep(),
	)
	db := newStrictTestDb(t, state)
	verifyResult := make(chan error, 1)
	go func() {
		_, err := db.VerifySchema(context.Background(), []any{&schemaOrchestratorRaceEntity{}}, &SchemaVerifyOptions{MaxConcurrency: 1})
		verifyResult <- err
	}()
	<-entered
	type transitionResult struct {
		transition *DatabaseGenerationTransition
		err        error
	}
	transitionDone := make(chan transitionResult, 1)
	go func() {
		transition, err := db.BeginDatabaseGenerationTransition("schema-next")
		transitionDone <- transitionResult{transition: transition, err: err}
	}()
	deadline := time.Now().Add(time.Second)
	for !db.generationUnavailable.Load() && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if !db.generationUnavailable.Load() {
		t.Fatal("generation transition did not reach barrier")
	}
	select {
	case result := <-transitionDone:
		t.Fatalf("generation transition crossed active schema lease: %+v", result)
	default:
	}
	close(releaseQuery)
	if err := <-verifyResult; err != nil {
		t.Fatalf("VerifySchema under generation lease: %v", err)
	}
	result := <-transitionDone
	if result.err != nil || result.transition == nil {
		t.Fatalf("generation transition after schema call: %+v", result)
	}
	if err := result.transition.Abort(); err != nil {
		t.Fatalf("abort generation transition: %v", err)
	}
}
