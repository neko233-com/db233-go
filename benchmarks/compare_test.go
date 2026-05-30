package benchmarks

import (
	"fmt"
	"testing"
	"time"

	"github.com/neko233-com/db233-go/pkg/db233"
)

// compareRow 框架对比一行结果（毫秒，越小越好）。
type compareRow struct {
	Framework       string
	SingleReadMs    float64
	Login3TableMs   float64
	BatchUpsert50Ms float64
	SessionRead1kMs float64
	RelSingleRead   float64 // 相对 database/sql
}

const (
	compareWarmup = 3
	compareRuns   = 15
)

// TestFrameworkCompare_Report 与 database/sql、sqlx、GORM 对比（需 MySQL）。
// 运行: cd benchmarks && go test -run TestFrameworkCompare_Report -timeout 3m -v
func TestFrameworkCompare_Report(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过框架对比")
	}
	env := openBenchEnv(t)
	defer env.SQL.Close()
	defer env.DB233.Close()

	playerID := benchIDPrefix + "001"
	rows := []compareRow{
		runCompareDB233(t, env, playerID),
		runCompareRawSQL(t, env, playerID),
		runCompareSQLX(t, env, playerID),
		runCompareGORM(t, env, playerID),
	}

	base := rows[1].SingleReadMs // database/sql 基线
	if base <= 0 {
		base = 1
	}
	for i := range rows {
		rows[i].RelSingleRead = rows[i].SingleReadMs / base
	}

	t.Log("\n========== Go 框架性能对比（中位数 ms，同机 MySQL）==========")
	t.Log(fmt.Sprintf("%-14s %12s %12s %14s %14s %8s",
		"框架", "单次读", "登录3表", "批量写50", "Session读1k", "读倍率"))
	for _, r := range rows {
		sessionCol := "-"
		if r.SessionRead1kMs >= 0 {
			sessionCol = fmt.Sprintf("%.3f", r.SessionRead1kMs)
		}
		t.Log(fmt.Sprintf("%-14s %12.3f %12.3f %14.3f %14s %7.2fx",
			r.Framework, r.SingleReadMs, r.Login3TableMs, r.BatchUpsert50Ms, sessionCol, r.RelSingleRead))
	}

	db233Row := rows[0]
	gormRow := rows[3]
	// 单次 PK 读：应接近 GORM（直扫字段 + Stmt 缓存），允许 15% 误差（RDS RTT 抖动）
	if gormRow.SingleReadMs > 0 && db233Row.SingleReadMs > gormRow.SingleReadMs*1.15 {
		t.Errorf("db233 单次读 %.3fms 慢于 GORM %.3fms 超过 15%%", db233Row.SingleReadMs, gormRow.SingleReadMs)
	}
	// db233 单次读应不超过 raw SQL 1.25x
	if db233Row.RelSingleRead > 1.25 {
		t.Errorf("db233 单次读 %.2fx 慢于 raw SQL 过多（阈值 1.25x）", db233Row.RelSingleRead)
	}
	// Session 读 1000 次应远快于 GORM 1000 次（游戏服路径）
	if gormRow.SingleReadMs > 0 && db233Row.SessionRead1kMs >= 0 {
		gorm1k := gormRow.SingleReadMs * 1000
		if db233Row.SessionRead1kMs > gorm1k/10 {
			t.Errorf("Session 读 1k=%.3fms 未显著优于 GORM 估算 %.0fms", db233Row.SessionRead1kMs, gorm1k)
		}
	}
	// 批量写应优于或接近 GORM
	if db233Row.BatchUpsert50Ms > gormRow.BatchUpsert50Ms*1.5 {
		t.Errorf("db233 批量写 %.3fms 慢于 GORM %.3fms 过多", db233Row.BatchUpsert50Ms, gormRow.BatchUpsert50Ms)
	}
}

func runCompareDB233(t *testing.T, env *benchEnv, playerID string) compareRow {
	t.Helper()
	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &BenchPlayerEntity{}})
	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &BenchBagEntity{}})
	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &BenchQuestEntity{}})
	SaveBenchEntityCache(t)
	SetBenchEntityCacheKey(t, "sessionFlushIntervalMs", 0)

	sr := db233.NewSessionRepository(env.Repo)
	defer sr.Stop()
	session, err := sr.OpenSession(playerID, []db233.IDbEntity{
		&BenchPlayerEntity{}, &BenchBagEntity{}, &BenchQuestEntity{},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	single := benchMedian(t, compareRuns, func() {
		_, _ = env.Repo.FindById(playerID, &BenchPlayerEntity{})
	})
	login := benchMedian(t, compareRuns, func() {
		_ = env.Repo.FindByIdConcurrent(playerID, []db233.IDbEntity{
			&BenchPlayerEntity{}, &BenchBagEntity{}, &BenchQuestEntity{},
		}, nil)
	})
	batch := benchMedian(t, compareRuns, func() {
		ents := make([]db233.IDbEntity, 50)
		for i := 0; i < 50; i++ {
			ents[i] = &BenchPlayerEntity{
				PlayerID: fmt.Sprintf("%s_b%d", benchIDPrefix, i),
				Name:     "b", Level: i,
			}
		}
		_ = env.Repo.UpdateBatchUpsert(ents)
	})
	sessionRead := benchMedian(t, compareRuns, func() {
		for i := 0; i < 1000; i++ {
			_ = session.Get(&BenchPlayerEntity{})
		}
	})

	return compareRow{
		Framework: "db233-go", SingleReadMs: single, Login3TableMs: login,
		BatchUpsert50Ms: batch, SessionRead1kMs: sessionRead,
	}
}

func runCompareRawSQL(t *testing.T, env *benchEnv, playerID string) compareRow {
	t.Helper()
	q := "SELECT playerId, name, level FROM " + benchTable + " WHERE playerId = ?"
	single := benchMedian(t, compareRuns, func() {
		var id, name string
		var level int
		_ = env.SQL.QueryRow(q, playerID).Scan(&id, &name, &level)
	})
	login := benchMedian(t, compareRuns, func() {
		var id, name string
		var level int
		_ = env.SQL.QueryRow(q, playerID).Scan(&id, &name, &level)
		var gold int
		_ = env.SQL.QueryRow("SELECT gold FROM "+benchBagTable+" WHERE playerId = ?", playerID).Scan(&gold)
		var qd string
		_ = env.SQL.QueryRow("SELECT questData FROM "+benchQuestTable+" WHERE playerId = ?", playerID).Scan(&qd)
	})
	batch := benchMedian(t, compareRuns, func() {
		tx, _ := env.SQL.Begin()
		stmt, _ := tx.Prepare("INSERT INTO " + benchTable + " (playerId,name,level) VALUES (?,?,?) ON DUPLICATE KEY UPDATE name=VALUES(name), level=VALUES(level)")
		for i := 0; i < 50; i++ {
			_, _ = stmt.Exec(fmt.Sprintf("%s_r%d", benchIDPrefix, i), "r", i)
		}
		_ = stmt.Close()
		_ = tx.Commit()
	})
	return compareRow{Framework: "database/sql", SingleReadMs: single, Login3TableMs: login, BatchUpsert50Ms: batch, SessionRead1kMs: -1}
}

func runCompareSQLX(t *testing.T, env *benchEnv, playerID string) compareRow {
	t.Helper()
	single := benchMedian(t, compareRuns, func() {
		var p sqlxPlayer
		_ = env.SQLX.Get(&p, "SELECT playerId, name, level FROM "+benchTable+" WHERE playerId = ?", playerID)
	})
	login := benchMedian(t, compareRuns, func() {
		var p sqlxPlayer
		var b sqlxBag
		var q sqlxQuest
		_ = env.SQLX.Get(&p, "SELECT playerId, name, level FROM "+benchTable+" WHERE playerId = ?", playerID)
		_ = env.SQLX.Get(&b, "SELECT playerId, gold FROM "+benchBagTable+" WHERE playerId = ?", playerID)
		_ = env.SQLX.Get(&q, "SELECT playerId, questData FROM "+benchQuestTable+" WHERE playerId = ?", playerID)
	})
	batch := benchMedian(t, compareRuns, func() {
		tx, _ := env.SQLX.Beginx()
		for i := 0; i < 50; i++ {
			_, _ = tx.Exec("INSERT INTO "+benchTable+" (playerId,name,level) VALUES (?,?,?) ON DUPLICATE KEY UPDATE name=VALUES(name), level=VALUES(level)",
				fmt.Sprintf("%s_x%d", benchIDPrefix, i), "x", i)
		}
		_ = tx.Commit()
	})
	return compareRow{Framework: "sqlx", SingleReadMs: single, Login3TableMs: login, BatchUpsert50Ms: batch, SessionRead1kMs: -1}
}

func runCompareGORM(t *testing.T, env *benchEnv, playerID string) compareRow {
	t.Helper()
	single := benchMedian(t, compareRuns, func() {
		var p gormPlayer
		_ = env.GORM.Where("playerId = ?", playerID).First(&p).Error
	})
	login := benchMedian(t, compareRuns, func() {
		var p gormPlayer
		var b gormBag
		var q gormQuest
		_ = env.GORM.Where("playerId = ?", playerID).First(&p).Error
		_ = env.GORM.Where("playerId = ?", playerID).First(&b).Error
		_ = env.GORM.Where("playerId = ?", playerID).First(&q).Error
	})
	batch := benchMedian(t, compareRuns, func() {
		rows := make([]gormPlayer, 50)
		for i := 0; i < 50; i++ {
			rows[i] = gormPlayer{PlayerID: fmt.Sprintf("%s_g%d", benchIDPrefix, i), Name: "g", Level: i}
		}
		_ = env.GORM.Save(&rows).Error
	})
	return compareRow{Framework: "GORM", SingleReadMs: single, Login3TableMs: login, BatchUpsert50Ms: batch, SessionRead1kMs: -1}
}

func benchMedian(t *testing.T, runs int, fn func()) float64 {
	t.Helper()
	for i := 0; i < compareWarmup; i++ {
		fn()
	}
	samples := make([]float64, runs)
	for i := 0; i < runs; i++ {
		start := time.Now()
		fn()
		samples[i] = float64(time.Since(start).Microseconds()) / 1000.0
	}
	return medianMs(samples)
}

func SaveBenchEntityCache(t *testing.T) {
	t.Helper()
	saved := db233.GetEntityCacheSettings().Snapshot()
	t.Cleanup(func() { db233.GetEntityCacheSettings().ApplyFull(saved) })
}

func SetBenchEntityCacheKey(t *testing.T, key string, value any) {
	t.Helper()
	snap := db233.GetEntityCacheSettings().Snapshot()
	var old any
	switch key {
	case "sessionFlushIntervalMs":
		old = snap.SessionFlushIntervalMs
	case "maxSessions":
		old = snap.MaxSessions
	case "flushOnEvict":
		old = snap.FlushOnEvict
	default:
		t.Fatalf("unsupported key %s", key)
	}
	_ = db233.GetEntityCacheSettings().Set(key, value)
	t.Cleanup(func() { _ = db233.GetEntityCacheSettings().Set(key, old) })
}
