package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

func TestLoadLocalDbConfigFromFile(t *testing.T) {
	path := filepath.Join("..", "config.local.json.example")
	cfg, err := db233.LoadLocalDbConfigFromFile(path)
	if err != nil {
		t.Fatalf("LoadLocalDbConfigFromFile 失败: %v", err)
	}
	if cfg.Host == "" || cfg.Port != 3306 {
		t.Fatalf("配置解析不正确: %+v", cfg)
	}
	dbCfg := cfg.ToDbConnectionConfig()
	if dbCfg.MaxOpenConns != 50 || dbCfg.MaxIdleConns != 10 {
		t.Errorf("连接池默认值不正确: open=%d idle=%d", dbCfg.MaxOpenConns, dbCfg.MaxIdleConns)
	}
}

func TestOpenDbFromLocalConfig_SkipIfMissing(t *testing.T) {
	_, _, err := db233.OpenDbFromLocalConfig("nonexistent.local.json")
	if err == nil {
		t.Fatal("不存在的文件应返回错误")
	}
}

func TestCreateTestDb_PrefersLocalConfig(t *testing.T) {
	localPath := filepath.Join("..", "config.local.json")
	if _, err := os.Stat(localPath); err != nil {
		t.Skip("无 config.local.json，跳过")
	}
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()
	if err := db.DataSource.Ping(); err != nil {
		t.Fatalf("Ping 失败: %v", err)
	}
}

func TestPlayerSession_IsResolved_NegativeCache(t *testing.T) {
	SaveEntityCacheSettings(t)
	SetEntityCacheKey(t, "sessionFlushIntervalMs", 0)
	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &TestBatchFindEntity{}})

	db := db233.NewDb(nil, 0, nil)
	sr := db233.NewSessionRepository(db233.NewBaseCrudRepository(db))
	defer sr.Stop()

	session, err := sr.OpenSession("neg1", []db233.IDbEntity{&TestBatchFindEntity{}})
	if err != nil {
		t.Fatalf("OpenSession 失败: %v", err)
	}
	EnableSessionNegativeCache(session)
	_, _ = session.GetOrLoad(&TestBatchFindEntity{})

	if !session.IsResolved(&TestBatchFindEntity{}) {
		t.Error("Session 负缓存开启后 GetOrLoad 无记录应 IsResolved")
	}
}
