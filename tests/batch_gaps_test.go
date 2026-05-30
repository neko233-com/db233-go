package tests

import (
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

func TestFindByIdsMap(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()
	if err := setupBatchFindTable(db); err != nil {
		t.Fatal(err)
	}
	defer db.DataSource.Exec("DELETE FROM test_batch_find WHERE playerId LIKE 'map_%'")

	repo := db233.NewBaseCrudRepository(db)
	_ = repo.UpdateBatchUpsert([]db233.IDbEntity{
		&TestBatchFindEntity{PlayerID: "map_1", Name: "a", Level: 1},
		&TestBatchFindEntity{PlayerID: "map_2", Name: "b", Level: 2},
	})

	m, err := repo.FindByIdsMap([]any{"map_1", "map_2", "map_missing"}, &TestBatchFindEntity{})
	if err != nil {
		t.Fatalf("FindByIdsMap 失败: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("期望 2 条，得到 %d", len(m))
	}
	if m["map_1"].(*TestBatchFindEntity).Level != 1 {
		t.Error("map_1 数据不对")
	}
}

func TestUpdateBatch_TrueBatch(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()
	if err := setupBatchFindTable(db); err != nil {
		t.Fatal(err)
	}
	defer db.DataSource.Exec("DELETE FROM test_batch_find WHERE playerId LIKE 'ub_%'")

	repo := db233.NewBaseCrudRepository(db)
	ents := []db233.IDbEntity{
		&TestBatchFindEntity{PlayerID: "ub_1", Name: "x", Level: 1},
		&TestBatchFindEntity{PlayerID: "ub_2", Name: "y", Level: 2},
	}
	if err := repo.UpdateBatchUpsert(ents); err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		e.(*TestBatchFindEntity).Level += 10
	}
	if err := repo.UpdateBatch(ents); err != nil {
		t.Fatalf("UpdateBatch 失败: %v", err)
	}
	got, _ := repo.FindByIdsMap([]any{"ub_1", "ub_2"}, &TestBatchFindEntity{})
	if got["ub_1"].(*TestBatchFindEntity).Level != 11 {
		t.Errorf("ub_1 level 期望 11")
	}
}

func TestPreparedStmtCache(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()
	if err := setupBatchFindTable(db); err != nil {
		t.Fatal(err)
	}

	cache := db233.GetPreparedStmtCache()
	cache.Clear()
	db233.GetCrudPerformanceSettings().ApplyFull(db233.DefaultCrudPerformanceSettings())

	repo := db233.NewBaseCrudRepository(db)
	_ = repo.UpdateBatchUpsert([]db233.IDbEntity{
		&TestBatchFindEntity{PlayerID: "stmt_1", Name: "s", Level: 1},
	})
	for i := 0; i < 5; i++ {
		_, _ = repo.FindById("stmt_1", &TestBatchFindEntity{})
	}
	if cache.Len() == 0 {
		t.Error("Stmt 缓存应有条目")
	}
}
