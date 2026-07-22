package db233

import (
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"
)

type productionMigrationEntity struct {
	ID   string `db:"id" primary_key:"true"`
	Name string `db:"name"`
}

func (*productionMigrationEntity) TableName() string       { return "production_migration_entity" }
func (*productionMigrationEntity) SerializeBeforeSaveDb()  {}
func (*productionMigrationEntity) DeserializeAfterLoadDb() {}

type productionIndexedMigrationEntity struct {
	ID   string `db:"id" primary_key:"true"`
	Name string `db:"name"`
}

func (*productionIndexedMigrationEntity) TableName() string {
	return "production_indexed_migration_entity"
}
func (*productionIndexedMigrationEntity) SerializeBeforeSaveDb()  {}
func (*productionIndexedMigrationEntity) DeserializeAfterLoadDb() {}
func (*productionIndexedMigrationEntity) GetTableMetaData() *TableMetaData {
	return &TableMetaData{Indexes: []*IndexMetaData{{
		IndexName: "idx_production_name",
		Columns:   []string{"name"},
	}}}
}

func newProductionCrudManager() *CrudManager {
	return &CrudManager{
		tableNamePkColNameListMap:   make(map[string][]string),
		tableNameToColNameMap:       make(map[string][]string),
		tableToPkToColValueMap:      make(map[string]map[any]map[string]any),
		metadataClassSet:            make(map[reflect.Type]bool),
		typeToPrimaryKeyColumnCache: make(map[reflect.Type]string),
	}
}

func TestAutoMigrateTableMissingTableDoesNotReenterCrudManagerLock(t *testing.T) {
	state := newScriptedDBState(
		scriptedStep{
			kind:    "query",
			columns: []string{"COUNT(*)"},
			rows:    [][]driver.Value{{int64(0)}},
		},
		scriptedStep{kind: "exec", queryContains: "CREATE TABLE"},
	)
	done := make(chan error, 1)
	go func() {
		done <- newProductionCrudManager().AutoMigrateTable(
			newStrictTestDb(t, state),
			&productionMigrationEntity{},
			NewSafeAutoDbPermission(),
		)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("migrate missing table: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AutoMigrateTable deadlocked while creating a missing table")
	}
	if state.countCalls("query") != 1 || state.countCalls("exec") != 1 {
		t.Fatalf("unexpected calls: %#v", state.snapshotCalls())
	}
}

func TestAutoMigrateTablePropagatesColumnDDLError(t *testing.T) {
	ddlErr := errors.New("alter column failed")
	state := newScriptedDBState(
		scriptedStep{
			kind:    "query",
			columns: []string{"COUNT(*)"},
			rows:    [][]driver.Value{{int64(1)}},
		},
		scriptedStep{
			kind:    "query",
			columns: []string{"COLUMN_NAME", "COLUMN_TYPE", "IS_NULLABLE", "COLUMN_KEY", "COLUMN_DEFAULT"},
			rows: [][]driver.Value{
				{"id", "varchar(255)", "NO", "PRI", nil},
			},
		},
		scriptedStep{kind: "exec", queryContains: "ADD COLUMN", execErr: ddlErr},
	)
	err := newProductionCrudManager().AutoMigrateTable(
		newStrictTestDb(t, state),
		&productionMigrationEntity{},
		NewSafeAutoDbPermission(),
	)
	if !errors.Is(err, ddlErr) {
		t.Fatalf("column DDL error was swallowed: %v", err)
	}
}

func TestAutoCreateTablePropagatesIndexDDLError(t *testing.T) {
	indexErr := errors.New("create index failed")
	state := newScriptedDBState(
		scriptedStep{
			kind:    "query",
			columns: []string{"COUNT(*)"},
			rows:    [][]driver.Value{{int64(0)}},
		},
		scriptedStep{kind: "exec", queryContains: "CREATE TABLE"},
		scriptedStep{
			kind:    "query",
			columns: []string{"INDEX_NAME", "COLUMNS", "NON_UNIQUE"},
		},
		scriptedStep{kind: "exec", queryContains: "CREATE INDEX", execErr: indexErr},
	)
	err := newProductionCrudManager().AutoCreateTable(
		newStrictTestDb(t, state),
		&productionIndexedMigrationEntity{},
	)
	if !errors.Is(err, indexErr) {
		t.Fatalf("index DDL error was swallowed: %v", err)
	}
}
