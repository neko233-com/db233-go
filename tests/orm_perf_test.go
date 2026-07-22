package tests

import (
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

type testFastOrmEntity struct {
	ID   int64  `db:"id" primary_key:"true"`
	Name string `db:"name"`
}

func (e *testFastOrmEntity) TableName() string                      { return "test_fast_orm" }
func (e *testFastOrmEntity) SerializeBeforeSaveDb()                 {}
func (e *testFastOrmEntity) DeserializeAfterLoadDb()                {}
func (e *testFastOrmEntity) GetTableMetaData() *db233.TableMetaData { return nil }

type testWarmEnt struct {
	ID int64 `db:"id" primary_key:"true"`
}

func (e *testWarmEnt) TableName() string                      { return "test_warm_ent" }
func (e *testWarmEnt) SerializeBeforeSaveDb()                 {}
func (e *testWarmEnt) DeserializeAfterLoadDb()                {}
func (e *testWarmEnt) GetTableMetaData() *db233.TableMetaData { return nil }

func TestFastOrmScan_FindById(t *testing.T) {
	db := CreateTestDb(t)
	defer db.Close()
	repo := db233.NewBaseCrudRepository(db)

	_ = db233.GetCrudPerformanceSettings().Set("enableFastOrmScan", true)

	_, _ = db.ExecuteUpdate("CREATE TABLE IF NOT EXISTS test_fast_orm (id BIGINT PRIMARY KEY, name VARCHAR(64))")
	_, _ = db.ExecuteUpdate("INSERT INTO test_fast_orm (id, name) VALUES (?, ?) ON DUPLICATE KEY UPDATE name=VALUES(name)", 9001, "fast")

	ent, err := repo.FindById(int64(9001), &testFastOrmEntity{})
	if err != nil {
		t.Fatalf("FindById: %v", err)
	}
	if ent == nil {
		t.Fatal("expected entity")
	}
	got := ent.(*testFastOrmEntity)
	if got.Name != "fast" {
		t.Fatalf("name=%q", got.Name)
	}

	plan, err := db233.GetOrmScanPlanCache().GetPlan(&testFastOrmEntity{}, []string{"id", "name"})
	if err != nil || plan == nil {
		t.Fatalf("scan plan: %v", err)
	}
}

func TestRowMapPool_QueryNamed(t *testing.T) {
	db := CreateTestDb(t)
	defer db.Close()
	_ = db233.GetCrudPerformanceSettings().Set("enableRowMapPool", true)

	_, _ = db.ExecuteUpdate("CREATE TABLE IF NOT EXISTS test_row_pool (id BIGINT PRIMARY KEY, v VARCHAR(32))")
	_, _ = db.ExecuteUpdate("INSERT INTO test_row_pool (id, v) VALUES (?, ?) ON DUPLICATE KEY UPDATE v=VALUES(v)", 1, "a")

	rows := db.QueryNamed("SELECT id, v FROM test_row_pool WHERE id={id}", map[string]any{"id": 1})
	if len(rows) != 1 {
		t.Fatalf("rows=%v", rows)
	}
	v := rows[0]["v"]
	switch x := v.(type) {
	case string:
		if x != "a" {
			t.Fatalf("v=%q", x)
		}
	case []byte:
		if string(x) != "a" {
			t.Fatalf("v=%q", x)
		}
	default:
		t.Fatalf("unexpected v type %T=%v", v, v)
	}
}

func TestColdStartWarmup(t *testing.T) {
	db := CreateTestDb(t)
	defer db.Close()

	_, _ = db.ExecuteUpdate("CREATE TABLE IF NOT EXISTS test_warm_ent (id BIGINT PRIMARY KEY)")

	err := db233.WarmGameDb(db, []db233.IDbEntity{&testWarmEnt{}})
	if err != nil {
		t.Fatalf("WarmGameDb: %v", err)
	}
	_, err = db233.GetOrmScanPlanCache().GetPlan(&testWarmEnt{}, []string{"id"})
	if err != nil {
		t.Fatalf("plan after warmup: %v", err)
	}
}
