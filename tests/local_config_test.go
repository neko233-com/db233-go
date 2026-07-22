package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

func TestIntegrationDatabaseHostGuard(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "localhost", "LOCALHOST", "::1", "[::1]"} {
		if !isLoopbackTestHost(host) {
			t.Fatalf("本机地址被拒绝: %q", host)
		}
	}
	for _, host := range []string{"", "database.internal", "example.com"} {
		if isLoopbackTestHost(host) {
			t.Fatalf("非本机地址被接受: %q", host)
		}
	}
}

func TestLoadLocalDbConfigFromFile(t *testing.T) {
	for _, fileName := range []string{"config.local.json", "config.local.yaml"} {
		t.Run(filepath.Ext(fileName), func(t *testing.T) {
			examplePath := filepath.Join("..", fileName+".example")
			data, err := os.ReadFile(examplePath)
			if err != nil {
				t.Fatalf("读取示例配置失败: %v", err)
			}
			path := filepath.Join(t.TempDir(), fileName)
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatalf("创建本地配置失败: %v", err)
			}
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
		})
	}
}

func TestLoadLocalDbConfigFromFile_RejectsWideUnixPermissions(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Windows 使用 DACL，不使用 Unix mode 位")
	}
	path := filepath.Join(t.TempDir(), "config.local.json")
	data := []byte(`{"host":"127.0.0.1","port":3306}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db233.LoadLocalDbConfigFromFile(path); err == nil {
		t.Fatal("权限过宽的本地凭据文件应被拒绝")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db233.LoadLocalDbConfigFromFile(path); err != nil {
		t.Fatalf("0600 本地配置应可读取: %v", err)
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
	SaveCacheableEntityRegistry(t)
	SetEntityCacheKey(t, "sessionFlushIntervalMs", 0)
	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &TestBatchFindEntity{}})

	sr := NewEmptyBatchFindSessionRepository(t, "neg1")

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
