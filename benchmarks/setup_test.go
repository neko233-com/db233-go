package benchmarks

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/neko233-com/db233-go/pkg/db233"
	"gorm.io/gorm"
)

const (
	benchTable      = "bench_framework_player"
	benchBagTable   = "bench_framework_bag"
	benchQuestTable = "bench_framework_quest"
	benchIDPrefix   = "bench_fw_"
)

// BenchPlayerEntity db233 实体
type BenchPlayerEntity struct {
	PlayerID string `db:"playerId" primary_key:"true"`
	Name     string `db:"name"`
	Level    int    `db:"level"`
}

func (e *BenchPlayerEntity) TableName() string                      { return benchTable }
func (e *BenchPlayerEntity) SerializeBeforeSaveDb()                 {}
func (e *BenchPlayerEntity) DeserializeAfterLoadDb()                {}
func (e *BenchPlayerEntity) GetTableMetaData() *db233.TableMetaData { return nil }

type BenchBagEntity struct {
	PlayerID string `db:"playerId" primary_key:"true"`
	Gold     int    `db:"gold"`
}

func (e *BenchBagEntity) TableName() string                      { return benchBagTable }
func (e *BenchBagEntity) SerializeBeforeSaveDb()                 {}
func (e *BenchBagEntity) DeserializeAfterLoadDb()                {}
func (e *BenchBagEntity) GetTableMetaData() *db233.TableMetaData { return nil }

type BenchQuestEntity struct {
	PlayerID  string `db:"playerId" primary_key:"true"`
	QuestData string `db:"questData"`
}

func (e *BenchQuestEntity) TableName() string                      { return benchQuestTable }
func (e *BenchQuestEntity) SerializeBeforeSaveDb()                 {}
func (e *BenchQuestEntity) DeserializeAfterLoadDb()                {}
func (e *BenchQuestEntity) GetTableMetaData() *db233.TableMetaData { return nil }

type benchEnv struct {
	SQL   *sql.DB
	SQLX  *sqlx.DB
	GORM  *gorm.DB
	DB233 *db233.Db
	Repo  *db233.BaseCrudRepository
	DSN   string
}

func openBenchEnv(t *testing.T) *benchEnv {
	t.Helper()
	dsn, sqlDB := openMySQL(t)
	db233.GetCrudPerformanceSettings().ApplyFull(db233.DefaultCrudPerformanceSettings())
	db233.GetEntityCacheSettings().ApplyFull(db233.DefaultEntityCacheSettings())

	db233DB := db233.NewDb(sqlDB, 0, nil)
	t.Cleanup(func() {
		if err := db233DB.Close(); err != nil {
			t.Errorf("关闭 db233 连接失败: %s", db233.SafeErrorSummary(err))
		}
	})
	db233.RegisterDbForConnectionPool(db233DB)
	if err := db233.WarmConnectionPool(sqlDB, 3); err != nil {
		t.Fatalf("连接池预热失败: %s", db233.SafeErrorSummary(err))
	}

	env := &benchEnv{
		SQL:   sqlDB,
		DB233: db233DB,
		Repo:  db233.NewBaseCrudRepository(db233DB),
		DSN:   dsn,
	}
	env.SQLX = mustSQLX(t, dsn)
	env.GORM = mustGORM(t, dsn)
	setupBenchTables(t, env)
	if err := seedBenchPlayerEnv(env, benchIDPrefix+"001"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db233.WarmGameDb(db233DB, []db233.IDbEntity{
		&BenchPlayerEntity{}, &BenchBagEntity{}, &BenchQuestEntity{},
	}); err != nil {
		t.Fatalf("ORM 预热失败: %s", db233.SafeErrorSummary(err))
	}
	return env
}

func openMySQL(t *testing.T) (string, *sql.DB) {
	t.Helper()
	if dsn := os.Getenv("DB233_TEST_DSN"); dsn != "" {
		requireLocalBenchDSN(t, dsn)
		ds, err := sql.Open("mysql", dsn)
		if err != nil {
			t.Skipf("无法打开 DB233_TEST_DSN: %v", err)
		}
		if err := ds.Ping(); err != nil {
			closeErr := ds.Close()
			t.Skipf("DB233_TEST_DSN 不可用: %s", db233.SafeErrorSummary(errors.Join(err, closeErr)))
		}
		return dsn, ds
	}
	for _, p := range []string{"../config.local.json", "config.local.json"} {
		if cfg, err := db233.LoadLocalDbConfigFromFile(p); err == nil {
			requireLoopbackBenchHost(t, cfg.Host)
			if err := ensureDB(cfg); err == nil {
				dbCfg := cfg.ToDbConnectionConfig()
				ds, err := dbCfg.CreateDataSource()
				if err == nil {
					return dbCfg.BuildDSN(), ds
				}
			}
		}
	}
	t.Skip("未配置 DB233_TEST_DSN 或忽略提交的 config.local.json")
	return "", nil
}

func requireLocalBenchDSN(t *testing.T, dsn string) {
	t.Helper()
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("DB233_TEST_DSN 非法: %v", err)
	}
	if cfg.Net == "unix" {
		return
	}
	host, _, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		t.Fatalf("DB233_TEST_DSN 地址非法: %v", err)
	}
	requireLoopbackBenchHost(t, host)
}

func requireLoopbackBenchHost(t *testing.T, host string) {
	t.Helper()
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		t.Fatalf("基准测试拒绝连接非本机数据库 host=%q", host)
	}
}

func ensureDB(cfg *db233.LocalDbConfigFile) (returnErr error) {
	if cfg.Database == "" {
		return nil
	}
	b := *cfg.ToDbConnectionConfig()
	b.Database = ""
	ds, err := b.CreateDataSource()
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, ds.Close()) }()
	_, err = ds.Exec(fmt.Sprintf(
		"CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", cfg.Database))
	return err
}

func setupBenchTables(t *testing.T, env *benchEnv) {
	t.Helper()
	stmts := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			playerId VARCHAR(64) NOT NULL PRIMARY KEY,
			name VARCHAR(255) NULL,
			level INT NOT NULL DEFAULT 0
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`, benchTable),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			playerId VARCHAR(64) NOT NULL PRIMARY KEY,
			gold INT NOT NULL DEFAULT 0
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`, benchBagTable),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			playerId VARCHAR(64) NOT NULL PRIMARY KEY,
			questData VARCHAR(255) NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`, benchQuestTable),
	}
	for _, s := range stmts {
		if _, err := env.SQL.Exec(s); err != nil {
			t.Fatalf("建表失败: %v", err)
		}
	}
	t.Cleanup(func() {
		for _, table := range []string{benchTable, benchBagTable, benchQuestTable} {
			if _, err := env.SQL.Exec("DELETE FROM "+table+" WHERE playerId LIKE ?", benchIDPrefix+"%"); err != nil {
				t.Errorf("清理 benchmark 表失败: %s", db233.SafeErrorSummary(err))
			}
		}
	})
}

func seedBenchPlayer(t *testing.T, env *benchEnv, playerID string) {
	t.Helper()
	if err := seedBenchPlayerEnv(env, playerID); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func seedBenchPlayerEnv(env *benchEnv, playerID string) error {
	return env.Repo.UpdateBatchUpsert([]db233.IDbEntity{
		&BenchPlayerEntity{PlayerID: playerID, Name: "bench", Level: 10},
		&BenchBagEntity{PlayerID: playerID, Gold: 100},
		&BenchQuestEntity{PlayerID: playerID, QuestData: "{}"},
	})
}

func medianMs(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	for i := 0; i < len(samples); i++ {
		for j := i + 1; j < len(samples); j++ {
			if samples[j] < samples[i] {
				samples[i], samples[j] = samples[j], samples[i]
			}
		}
	}
	return samples[len(samples)/2]
}

func configPathExists() bool {
	for _, p := range []string{"../config.local.json", "config.local.json"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	_, err := os.Stat(filepath.Join("..", "config.local.json.example"))
	return err == nil
}
