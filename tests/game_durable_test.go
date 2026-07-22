package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

func TestEntityTypeRegistry_SerializeRoundtrip(t *testing.T) {
	db233.GetEntityTypeRegistry().Register(&TestBatchFindEntity{})

	original := &TestBatchFindEntity{PlayerID: "p1", Name: "test", Level: 99}
	data, err := db233.SerializeEntity(original)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	restored, err := db233.DeserializeEntity("TestBatchFindEntity", data)
	if err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	player := restored.(*TestBatchFindEntity)
	if player.PlayerID != "p1" || player.Level != 99 {
		t.Errorf("往返不一致: %+v", player)
	}
}

func TestLocalWriteJournal_AppendAndRemove(t *testing.T) {
	dir := t.TempDir()
	db := db233.NewDb(nil, 0, nil)
	repo := db233.NewBaseCrudRepository(db)
	journal := db233.NewLocalWriteJournal(dir, repo)
	t.Cleanup(journal.Stop)

	db233.GetEntityTypeRegistry().Register(&TestBatchFindEntity{})

	entities := []db233.IDbEntity{
		&TestBatchFindEntity{PlayerID: "j1", Name: "a", Level: 1},
		&TestBatchFindEntity{PlayerID: "j2", Name: "b", Level: 2},
	}
	entries, err := journal.AppendEntities("SaveBatchUpsert", entities)
	if err != nil {
		t.Fatalf("AppendEntities 失败: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("期望 2 条 WAL，得到 %d", len(entries))
	}

	path := filepath.Join(dir, "pending.ndjson")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("WAL 文件应存在")
	}

	count, err := journal.PendingCount()
	if err != nil || count != 2 {
		t.Fatalf("PendingCount 期望 2，得到 %d, err=%v", count, err)
	}

	ids := []string{entries[0].ID, entries[1].ID}
	if err := journal.RemoveEntries(ids); err != nil {
		t.Fatalf("RemoveEntries 失败: %v", err)
	}
	count, _ = journal.PendingCount()
	if count != 0 {
		t.Fatalf("删除后 PendingCount 应为 0，得到 %d", count)
	}
}

func TestPlayerSession_SessionRepository(t *testing.T) {
	db := db233.NewDb(nil, 0, nil)
	repo := db233.NewBaseCrudRepository(db)
	sr := db233.NewSessionRepository(repo)
	if sr.OnlineCount() != 0 {
		t.Error("初始在线数应为 0")
	}
}

func TestApplyConnectionPoolSettings(t *testing.T) {
	// 无 DB 时仅验证不 panic
	settings := db233.DefaultCrudPerformanceSettings()
	settings.MaxOpenConns = 50
	settings.MaxIdleConns = 10
	db233.ApplyConnectionPoolSettings(nil, settings)
}

func TestDefaultGameDbOptions(t *testing.T) {
	opts := db233.DefaultGameDbOptions()
	if !opts.EnableLocalJournal || !opts.EnableWriteBuffer {
		t.Error("默认应启用 WAL 和写缓冲")
	}
}

func TestFaultTolerantManager_NeverDropDefault(t *testing.T) {
	ftm := db233.NewFaultTolerantManager(nil, nil)
	ftm.SetNeverDropFailedOps(true)
	// 默认 maxRetryAttempts=0 表示无限重试
}
