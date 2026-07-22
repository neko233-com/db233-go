package tests

import (
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

type testAllocPoolEntity struct {
	ID   int64  `db:"id" primary_key:"true"`
	Name string `db:"name"`
}

func (e *testAllocPoolEntity) TableName() string                      { return "test_alloc_pool" }
func (e *testAllocPoolEntity) SerializeBeforeSaveDb()                 {}
func (e *testAllocPoolEntity) DeserializeAfterLoadDb()                {}
func (e *testAllocPoolEntity) GetTableMetaData() *db233.TableMetaData { return nil }

func TestAllocPool_BatchUpsertAndFindByIds(t *testing.T) {
	db := CreateTestDb(t)
	defer db.Close()
	_ = db233.GetCrudPerformanceSettings().Set("enableAllocPool", true)
	repo := db233.NewBaseCrudRepository(db)

	_, _ = db.ExecuteUpdate(`CREATE TABLE IF NOT EXISTS test_alloc_pool (
		id BIGINT PRIMARY KEY, name VARCHAR(64))`)
	t.Cleanup(func() { _, _ = db.ExecuteUpdate("DROP TABLE IF EXISTS test_alloc_pool") })

	ents := []db233.IDbEntity{
		&testAllocPoolEntity{ID: 7001, Name: "a"},
		&testAllocPoolEntity{ID: 7002, Name: "b"},
	}
	if err := repo.UpdateBatchUpsert(ents); err != nil {
		t.Fatalf("UpdateBatchUpsert: %v", err)
	}

	list, err := repo.FindByIds([]any{int64(7001), int64(7002)}, &testAllocPoolEntity{})
	if err != nil {
		t.Fatalf("FindByIds: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("FindByIds len=%d", len(list))
	}
}

type testAllocOffEntity struct {
	ID   int64  `db:"id" primary_key:"true"`
	Name string `db:"name"`
}

func (e *testAllocOffEntity) TableName() string                      { return "test_alloc_off" }
func (e *testAllocOffEntity) SerializeBeforeSaveDb()                 {}
func (e *testAllocOffEntity) DeserializeAfterLoadDb()                {}
func (e *testAllocOffEntity) GetTableMetaData() *db233.TableMetaData { return nil }

func TestAllocPool_DisabledFallback(t *testing.T) {
	db := CreateTestDb(t)
	defer db.Close()
	_ = db233.GetCrudPerformanceSettings().Set("enableAllocPool", false)
	_ = db233.GetCrudPerformanceSettings().Set("enableRowMapPool", false)
	repo := db233.NewBaseCrudRepository(db)

	_, _ = db.ExecuteUpdate("CREATE TABLE IF NOT EXISTS test_alloc_off (id BIGINT PRIMARY KEY, name VARCHAR(64))")
	t.Cleanup(func() { _, _ = db.ExecuteUpdate("DROP TABLE IF EXISTS test_alloc_off") })

	ent := &testAllocOffEntity{ID: 8001, Name: "off"}
	if err := repo.Save(ent); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := repo.FindById(int64(8001), &testAllocOffEntity{})
	if err != nil || got == nil {
		t.Fatalf("FindById: %v got=%v", err, got)
	}
}
