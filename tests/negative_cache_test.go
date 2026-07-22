package tests

import (
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

func TestNegativeCache_DefaultOff(t *testing.T) {
	SaveEntityCacheSettings(t)
	SaveCacheableEntityRegistry(t)
	_ = db233.GetEntityCacheSettings().Set("negativeCacheEnabled", false)
	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &TestBatchFindEntity{}})

	sr := NewEmptyBatchFindSessionRepository(t, "neg_off")

	session, err := sr.OpenSession("neg_off", []db233.IDbEntity{&TestBatchFindEntity{}})
	if err != nil {
		t.Fatalf("OpenSession 失败: %v", err)
	}
	if session.IsResolved(&TestBatchFindEntity{}) {
		t.Error("负缓存默认关闭时，无记录不应 IsResolved")
	}
	if session.NegativeCacheEnabled() {
		t.Error("默认应未启用负缓存")
	}
}

func TestNegativeCache_SessionOverride(t *testing.T) {
	SaveEntityCacheSettings(t)
	SaveCacheableEntityRegistry(t)
	_ = db233.GetEntityCacheSettings().Set("negativeCacheEnabled", false)

	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &TestBatchFindEntity{}})
	sr := NewEmptyBatchFindSessionRepository(t, "neg_sess")

	session, err := sr.OpenSession("neg_sess", []db233.IDbEntity{&TestBatchFindEntity{}})
	if err != nil {
		t.Fatalf("OpenSession 失败: %v", err)
	}
	if session.IsResolved(&TestBatchFindEntity{}) {
		t.Error("全局关闭时 OpenSession 不应负缓存")
	}

	EnableSessionNegativeCache(session)
	_, _ = session.GetOrLoad(&TestBatchFindEntity{})
	if !session.IsResolved(&TestBatchFindEntity{}) {
		t.Error("Session 开启负缓存后 GetOrLoad 无记录应 IsResolved")
	}

	session.ClearNegativeCacheOverride()
	if session.NegativeCacheEnabled() {
		t.Error("Clear 后应跟随全局 false")
	}
}

func TestNegativeCache_GlobalOnSessionOff(t *testing.T) {
	SaveEntityCacheSettings(t)
	SaveCacheableEntityRegistry(t)
	_ = db233.GetEntityCacheSettings().Set("negativeCacheEnabled", true)

	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &TestBatchFindEntity{}})
	sr := NewEmptyBatchFindSessionRepository(t, "neg_global")

	session, err := sr.OpenSession("neg_global", []db233.IDbEntity{&TestBatchFindEntity{}})
	if err != nil {
		t.Fatalf("OpenSession 失败: %v", err)
	}
	if !session.IsResolved(&TestBatchFindEntity{}) {
		t.Error("全局开启负缓存时 Load 后应 IsResolved")
	}

	session.SetNegativeCacheEnabled(false)
	if session.IsResolved(&TestBatchFindEntity{}) {
		t.Error("Session 强制关闭负缓存后 IsResolved 应仅看正缓存")
	}
}

func TestNegativeCache_DynamicSet(t *testing.T) {
	SaveEntityCacheSettings(t)
	if err := db233.GetEntityCacheSettings().Set("negativeCacheEnabled", true); err != nil {
		t.Fatal(err)
	}
	if !db233.GetEntityCacheSettings().Snapshot().IsNegativeCacheEnabled() {
		t.Error("Set 应动态开启负缓存")
	}
	if err := db233.GetEntityCacheSettings().Set("negativeCacheEnabled", false); err != nil {
		t.Fatal(err)
	}
	if db233.GetEntityCacheSettings().Snapshot().IsNegativeCacheEnabled() {
		t.Error("Set 应动态关闭负缓存")
	}
}

func TestSaveBatchUpsert_MixedTableGrouping(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()
	if err := setupBatchFindTable(db); err != nil {
		t.Fatal(err)
	}
	if err := setupConcurrentLoginTables(db); err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.DataSource.Exec("DELETE FROM test_batch_find WHERE playerId LIKE 'mix_%'")
		db.DataSource.Exec("DELETE FROM test_player_bag WHERE playerId LIKE 'mix_%'")
	}()

	repo := db233.NewBaseCrudRepository(db)
	pid := "mix_001"
	if err := repo.UpdateBatchUpsert([]db233.IDbEntity{
		&TestBatchFindEntity{PlayerID: pid, Name: "a", Level: 1},
		&TestPlayerBagEntity{PlayerID: pid, Gold: 99},
	}); err != nil {
		t.Fatalf("混合表 BatchUpsert 失败: %v", err)
	}
	base, err := repo.FindById(pid, &TestBatchFindEntity{})
	if err != nil || base == nil {
		t.Fatal("base 应存在")
	}
	bag, err := repo.FindById(pid, &TestPlayerBagEntity{})
	if err != nil || bag == nil {
		t.Fatal("bag 应存在")
	}
	if bag.(*TestPlayerBagEntity).Gold != 99 {
		t.Errorf("gold 期望 99")
	}
}
