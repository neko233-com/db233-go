package tests

import (
	"fmt"
	"testing"
	"time"

	"github.com/neko233-com/db233-go/pkg/db233"
)

// TestBatchFindEntity 用于 FindByIds / SaveBatchUpsert 测试
type TestBatchFindEntity struct {
	PlayerID string `json:"playerId" db:"playerId" primary_key:"true"`
	Name     string `json:"name" db:"name"`
	Level    int    `json:"level" db:"level"`
}

func (e *TestBatchFindEntity) TableName() string { return "test_batch_find" }
func (e *TestBatchFindEntity) SerializeBeforeSaveDb() {}
func (e *TestBatchFindEntity) DeserializeAfterLoadDb() {}
func (e *TestBatchFindEntity) GetTableMetaData() *db233.TableMetaData { return nil }

type TestPlayerBagEntity struct {
	PlayerID string `db:"playerId" primary_key:"true"`
	Gold     int    `db:"gold"`
}

func (e *TestPlayerBagEntity) TableName() string { return "test_player_bag" }
func (e *TestPlayerBagEntity) SerializeBeforeSaveDb() {}
func (e *TestPlayerBagEntity) DeserializeAfterLoadDb() {}
func (e *TestPlayerBagEntity) GetTableMetaData() *db233.TableMetaData { return nil }

type TestPlayerQuestEntity struct {
	PlayerID string `db:"playerId" primary_key:"true"`
	QuestData string `db:"questData"`
}

func (e *TestPlayerQuestEntity) TableName() string { return "test_player_quest" }
func (e *TestPlayerQuestEntity) SerializeBeforeSaveDb() {}
func (e *TestPlayerQuestEntity) DeserializeAfterLoadDb() {}
func (e *TestPlayerQuestEntity) GetTableMetaData() *db233.TableMetaData { return nil }

func setupBatchFindTable(db *db233.Db) error {
	_, err := db.DataSource.Exec(`
		CREATE TABLE IF NOT EXISTS test_batch_find (
			playerId VARCHAR(64) NOT NULL PRIMARY KEY,
			name VARCHAR(255) NULL,
			level INT NOT NULL DEFAULT 0
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)
	return err
}

func setupConcurrentLoginTables(db *db233.Db) error {
	_, err := db.DataSource.Exec(`
		CREATE TABLE IF NOT EXISTS test_player_bag (
			playerId VARCHAR(64) NOT NULL PRIMARY KEY,
			gold INT NOT NULL DEFAULT 0
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
		CREATE TABLE IF NOT EXISTS test_player_quest (
			playerId VARCHAR(64) NOT NULL PRIMARY KEY,
			questData VARCHAR(255) NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)
	return err
}

func TestFindByIds(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	if err := setupBatchFindTable(db); err != nil {
		t.Fatalf("创建测试表失败: %v", err)
	}
	defer db.DataSource.Exec("DROP TABLE IF EXISTS test_batch_find")

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestBatchFindEntity{})

	entities := []db233.IDbEntity{
		&TestBatchFindEntity{PlayerID: "p001", Name: "Alice", Level: 1},
		&TestBatchFindEntity{PlayerID: "p002", Name: "Bob", Level: 2},
		&TestBatchFindEntity{PlayerID: "p003", Name: "Carol", Level: 3},
	}
	if err := repo.SaveBatchUpsert(entities); err != nil {
		t.Fatalf("准备数据失败: %v", err)
	}

	found, err := repo.FindByIds([]any{"p001", "p003", "p999"}, &TestBatchFindEntity{})
	if err != nil {
		t.Fatalf("FindByIds 失败: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("期望找到 2 条记录，得到 %d", len(found))
	}

	empty, err := repo.FindByIds([]any{}, &TestBatchFindEntity{})
	if err != nil {
		t.Fatalf("空 ID 列表应返回 nil 错误: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("空 ID 列表应返回空切片")
	}
}

func TestSaveBatchUpsert(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	if err := setupBatchFindTable(db); err != nil {
		t.Fatalf("创建测试表失败: %v", err)
	}
	defer db.DataSource.Exec("DROP TABLE IF EXISTS test_batch_find")

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestBatchFindEntity{})

	// 首次插入
	batch1 := []db233.IDbEntity{
		&TestBatchFindEntity{PlayerID: "u100", Name: "初始", Level: 1},
		&TestBatchFindEntity{PlayerID: "u101", Name: "初始", Level: 1},
	}
	if err := repo.SaveBatchUpsert(batch1); err != nil {
		t.Fatalf("首次 SaveBatchUpsert 失败: %v", err)
	}

	// 再次 UPSERT 更新
	batch2 := []db233.IDbEntity{
		&TestBatchFindEntity{PlayerID: "u100", Name: "更新", Level: 10},
		&TestBatchFindEntity{PlayerID: "u101", Name: "更新", Level: 20},
	}
	if err := repo.SaveBatchUpsert(batch2); err != nil {
		t.Fatalf("二次 SaveBatchUpsert 失败: %v", err)
	}

	found, err := repo.FindByIds([]any{"u100", "u101"}, &TestBatchFindEntity{})
	if err != nil {
		t.Fatalf("验证查询失败: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("期望 2 条记录，得到 %d", len(found))
	}

	levelMap := make(map[string]int)
	for _, e := range found {
		player := e.(*TestBatchFindEntity)
		levelMap[player.PlayerID] = player.Level
	}
	if levelMap["u100"] != 10 || levelMap["u101"] != 20 {
		t.Errorf("UPSERT 后等级不正确: %+v", levelMap)
	}
}

func TestFindByIdConcurrent(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	if err := setupConcurrentLoginTables(db); err != nil {
		t.Fatalf("创建测试表失败: %v", err)
	}
	defer func() {
		db.DataSource.Exec("DROP TABLE IF EXISTS test_player_bag")
		db.DataSource.Exec("DROP TABLE IF EXISTS test_player_quest")
	}()

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestPlayerBagEntity{})
	cm.AutoInitEntity(&TestPlayerQuestEntity{})

	playerID := fmt.Sprintf("login_%d", time.Now().UnixNano())
	if err := repo.Save(&TestPlayerBagEntity{PlayerID: playerID, Gold: 999}); err != nil {
		t.Fatalf("保存 bag 失败: %v", err)
	}
	if err := repo.Save(&TestPlayerQuestEntity{PlayerID: playerID, QuestData: "main=1"}); err != nil {
		t.Fatalf("保存 quest 失败: %v", err)
	}

	entityTypes := []db233.IDbEntity{
		&TestPlayerBagEntity{},
		&TestPlayerQuestEntity{},
		&TestPlayerBagEntity{}, // 重复表类型，模拟多模块同表
	}

	results := repo.FindByIdConcurrent(playerID, entityTypes, db233.NewDefaultConcurrentCrudConfig())
	if len(results) != 3 {
		t.Fatalf("期望 3 个结果，得到 %d", len(results))
	}
	for i, item := range results {
		if item.Err != nil {
			t.Fatalf("结果[%d] 出错: %v", i, item.Err)
		}
		if item.Entity == nil {
			t.Fatalf("结果[%d] 应找到实体", i)
		}
	}

	bag := results[0].Entity.(*TestPlayerBagEntity)
	if bag.Gold != 999 {
		t.Errorf("bag gold 期望 999，得到 %d", bag.Gold)
	}
	quest := results[1].Entity.(*TestPlayerQuestEntity)
	if quest.QuestData != "main=1" {
		t.Errorf("quest data 期望 main=1，得到 %s", quest.QuestData)
	}
}

func TestFindByIdConcurrent_SequentialFallback(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	if err := setupConcurrentLoginTables(db); err != nil {
		t.Fatalf("创建测试表失败: %v", err)
	}
	defer func() {
		db.DataSource.Exec("DROP TABLE IF EXISTS test_player_bag")
		db.DataSource.Exec("DROP TABLE IF EXISTS test_player_quest")
	}()

	repo := db233.NewBaseCrudRepository(db)
	playerID := "seq_player"
	_ = repo.Save(&TestPlayerBagEntity{PlayerID: playerID, Gold: 1})

	config := &db233.ConcurrentCrudConfig{EnableConcurrent: false}
	results := repo.FindByIdConcurrent(playerID, []db233.IDbEntity{&TestPlayerBagEntity{}}, config)
	if len(results) != 1 || results[0].Err != nil || results[0].Entity == nil {
		t.Fatalf("顺序模式 FindByIdConcurrent 失败: %+v", results)
	}
}
