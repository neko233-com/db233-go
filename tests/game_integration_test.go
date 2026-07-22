package tests

import (
	"path/filepath"
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

func TestLocalWriteJournal_DedupeSamePrimaryKey(t *testing.T) {
	dir := t.TempDir()
	db := db233.NewDb(nil, 0, nil)
	repo := db233.NewBaseCrudRepository(db)
	journal := db233.NewLocalWriteJournal(dir, repo)
	t.Cleanup(journal.Stop)
	db233.GetEntityTypeRegistry().Register(&TestBatchFindEntity{})

	e1 := &TestBatchFindEntity{PlayerID: "same", Name: "v1", Level: 1}
	e2 := &TestBatchFindEntity{PlayerID: "same", Name: "v2", Level: 2}

	if _, err := journal.AppendEntities("SaveBatchUpsert", []db233.IDbEntity{e1}); err != nil {
		t.Fatalf("第一次 Append 失败: %v", err)
	}
	if _, err := journal.AppendEntities("SaveBatchUpsert", []db233.IDbEntity{e2}); err != nil {
		t.Fatalf("第二次 Append 失败: %v", err)
	}

	count, err := journal.PendingCount()
	if err != nil {
		t.Fatalf("PendingCount 失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("同主键应合并为 1 条 WAL，得到 %d", count)
	}
}

func TestLocalWriteJournal_ReplayWithoutDB(t *testing.T) {
	dir := t.TempDir()
	repo := db233.NewBaseCrudRepository(nil)
	journal := db233.NewLocalWriteJournal(dir, repo)
	t.Cleanup(journal.Stop)
	db233.GetEntityTypeRegistry().Register(&TestBatchFindEntity{})

	_, err := journal.AppendEntities("SaveBatchUpsert", []db233.IDbEntity{
		&TestBatchFindEntity{PlayerID: "r1", Name: "x", Level: 1},
	})
	if err != nil {
		t.Fatalf("Append 失败: %v", err)
	}

	success, failed := journal.ReplayAll()
	if success != 0 || failed != 1 {
		t.Fatalf("无 DB 时应跳过回放: success=%d failed=%d", success, failed)
	}
	count, _ := journal.PendingCount()
	if count != 1 {
		t.Fatalf("WAL 条目应保留: count=%d", count)
	}
}

func TestSaveBatchUpsertWithoutDB_ReturnsError(t *testing.T) {
	repo := db233.NewBaseCrudRepository(nil)
	err := repo.SaveBatchUpsert([]db233.IDbEntity{
		&TestBatchFindEntity{PlayerID: "x", Name: "n", Level: 1},
	})
	if err == nil {
		t.Fatal("无 DB 连接时应返回错误")
	}
}

func TestInitGameDb_Integration(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	journalDir := filepath.Join(t.TempDir(), "journal")
	opts := db233.DefaultGameDbOptions()
	opts.LocalJournalPath = journalDir
	opts.EnableLocalJournal = true
	opts.EnableWriteBuffer = true
	opts.EntityTypes = []db233.IDbEntity{
		&TestBatchFindEntity{},
		&TestPlayerBagEntity{},
	}

	if _, err := db.DataSource.Exec(`
		CREATE TABLE IF NOT EXISTS test_batch_find (
			playerId VARCHAR(64) NOT NULL PRIMARY KEY,
			name VARCHAR(255) NULL,
			level INT NOT NULL DEFAULT 0
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if _, err := db.DataSource.Exec(`
		CREATE TABLE IF NOT EXISTS test_player_bag (
			playerId VARCHAR(64) NOT NULL PRIMARY KEY,
			gold INT NOT NULL DEFAULT 0
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	defer func() {
		db.DataSource.Exec("DROP TABLE IF EXISTS test_batch_find")
		db.DataSource.Exec("DROP TABLE IF EXISTS test_player_bag")
	}()

	dbConfig := db233.NewDefaultMySQLConfig("127.0.0.1", 3306, "root", "root", "db233_go")
	if _, err := db233.InitGameDb(db, dbConfig, opts); err != nil {
		t.Fatalf("InitGameDb 失败: %v", err)
	}

	repo := db233.NewBaseCrudRepository(db)
	if repo.GetWriteJournal() == nil {
		t.Fatal("InitGameDb 应绑定 WAL")
	}

	playerID := "game_int_001"
	if err := repo.UpdateBatchUpsert([]db233.IDbEntity{
		&TestBatchFindEntity{PlayerID: playerID, Name: "hero", Level: 10},
		&TestPlayerBagEntity{PlayerID: playerID, Gold: 500},
	}); err != nil {
		t.Fatalf("UpdateBatchUpsert 失败: %v", err)
	}

	sessionRepo := db.SessionRepo
	if sessionRepo == nil {
		sessionRepo = db233.NewSessionRepository(repo)
	}
	session, err := sessionRepo.OpenSession(playerID, []db233.IDbEntity{
		&TestBatchFindEntity{},
		&TestPlayerBagEntity{},
	})
	if err != nil {
		t.Fatalf("OpenSession 失败: %v", err)
	}

	base := session.Get(&TestBatchFindEntity{})
	if base == nil {
		t.Fatal("Session L1 应加载玩家基础数据")
	}
	if base.(*TestBatchFindEntity).Level != 10 {
		t.Errorf("Level 期望 10，得到 %d", base.(*TestBatchFindEntity).Level)
	}

	bag := session.Get(&TestPlayerBagEntity{}).(*TestPlayerBagEntity)
	bag.Gold += 100
	if err := session.MarkDirty(bag); err != nil {
		t.Fatalf("MarkDirty 失败: %v", err)
	}
	if session.DirtyCount() == 0 {
		t.Error("MarkDirty 后 dirty 应 > 0")
	}

	if err := sessionRepo.CloseSession(playerID); err != nil {
		t.Fatalf("CloseSession 失败: %v", err)
	}

	found, err := repo.FindById(playerID, &TestPlayerBagEntity{})
	if err != nil || found == nil {
		t.Fatalf("落库后应能查到 bag: err=%v", err)
	}
	if found.(*TestPlayerBagEntity).Gold != 600 {
		t.Errorf("Gold 期望 600，得到 %d", found.(*TestPlayerBagEntity).Gold)
	}

	if db.WriteJournal != nil {
		count, err := db.WriteJournal.PendingCount()
		if err != nil {
			t.Fatalf("PendingCount 失败: %v", err)
		}
		if count != 0 {
			t.Fatalf("成功落库后 WAL pending 应为 0，得到 %d", count)
		}
	}
}

func TestGameHighPerformance_FullCRUD(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	if err := setupBatchFindTable(db); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	defer db.DataSource.Exec("DROP TABLE IF EXISTS test_batch_find")

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestBatchFindEntity{})

	// SaveBatchUpsert + FindByIds
	batch := []db233.IDbEntity{
		&TestBatchFindEntity{PlayerID: "hp1", Name: "a", Level: 1},
		&TestBatchFindEntity{PlayerID: "hp2", Name: "b", Level: 2},
		&TestBatchFindEntity{PlayerID: "hp3", Name: "c", Level: 3},
	}
	if err := repo.UpdateBatchUpsert(batch); err != nil {
		t.Fatalf("UpdateBatchUpsert 失败: %v", err)
	}

	found, err := repo.FindByIds([]any{"hp1", "hp3"}, &TestBatchFindEntity{})
	if err != nil || len(found) != 2 {
		t.Fatalf("FindByIds 失败: len=%d err=%v", len(found), err)
	}

	// FindByIdConcurrent
	results := repo.FindByIdConcurrent("hp2", []db233.IDbEntity{&TestBatchFindEntity{}}, nil)
	if len(results) != 1 || results[0].Err != nil || results[0].Entity == nil {
		t.Fatalf("FindByIdConcurrent 失败: %+v", results)
	}

	// SaveBuffered + Flush
	entity := &TestBatchFindEntity{PlayerID: "hp1", Name: "updated", Level: 99}
	if err := repo.SaveBuffered(entity); err != nil {
		t.Fatalf("SaveBuffered 失败: %v", err)
	}
	if err := repo.FlushWriteBuffer(); err != nil {
		t.Fatalf("FlushWriteBuffer 失败: %v", err)
	}

	reloaded, _ := repo.FindById("hp1", &TestBatchFindEntity{})
	if reloaded.(*TestBatchFindEntity).Level != 99 {
		t.Errorf("Flush 后期望 level=99")
	}
}

func TestConnectionPool_RegisterDb(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	db233.RegisterDbForConnectionPool(db)
	stats := db.DataSource.Stats()
	if stats.MaxOpenConnections <= 0 {
		t.Error("连接池应已配置 MaxOpenConnections")
	}
}
