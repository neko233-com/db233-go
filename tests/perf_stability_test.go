package tests

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/neko233-com/db233-go/pkg/db233"
)

// perfReport 压测报告（单次运行，约 15–30s，需真实 MySQL）。
type perfReport struct {
	Host              string
	PingAvg           time.Duration
	PingMax           time.Duration
	FindByIdOnce      time.Duration
	CacheGet1000      time.Duration
	CacheSpeedup      float64
	Sequential3Find   time.Duration
	Concurrent3Find   time.Duration
	BatchUpsert50     time.Duration
	SingleSave50      time.Duration
	SessionCycle20    time.Duration
	DeferredPut100    time.Duration
	FlushOnce         time.Duration
	PoolInUse         int
	PoolOpen          int
	Recommendations   []string
}

const perfPlayerPrefix = "perf_stab_"

// TestPerfStability_Short 短时压测：MySQL 延迟、缓存收益、批量写、Session 生命周期稳定性。
// 运行: go test ./tests/ -run TestPerfStability_Short -timeout 90s -v
// CI 快速模式跳过: go test -short ./tests/...
func TestPerfStability_Short(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过压测，完整运行: go test ./tests/ -run TestPerfStability_Short -v")
	}

	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	if err := setupBatchFindTable(db); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := setupConcurrentLoginTables(db); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	defer cleanupPerfTables(db)

	db233.RegisterDbForConnectionPool(db)
	if err := db233.WarmConnectionPool(db.DataSource, 3); err != nil {
		t.Fatalf("连接池预热失败: %v", err)
	}

	// 压测仅动态改必要项，不 ApplyFull 覆盖生产配置
	SaveEntityCacheSettings(t)
	SetEntityCacheKey(t, "sessionFlushIntervalMs", 0)
	SetEntityCacheKey(t, "flushOnEvict", true)

	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &TestBatchFindEntity{}})
	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &TestPlayerBagEntity{}})
	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &TestPlayerQuestEntity{}})

	repo := db233.NewBaseCrudRepository(db)
	report := &perfReport{}

	// 1. MySQL Ping 延迟
	report.PingAvg, report.PingMax = measurePingLatency(db, 8)
	t.Logf("[延迟] Ping avg=%v max=%v", report.PingAvg, report.PingMax)

	// 2. 准备测试数据
	playerID := perfPlayerPrefix + "001"
	seedPlayerData(t, repo, playerID)

	// 3. FindById 单次
	report.FindByIdOnce = measure(func() {
		_, err := repo.FindById(playerID, &TestBatchFindEntity{})
		if err != nil {
			t.Fatalf("FindById 失败: %v", err)
		}
	})
	t.Logf("[读] FindById 单次=%v", report.FindByIdOnce)

	// 4. Session 缓存 vs DB
	sr := db233.NewSessionRepository(repo)
	defer sr.Stop()

	session, err := sr.OpenSession(playerID, []db233.IDbEntity{
		&TestBatchFindEntity{},
		&TestPlayerBagEntity{},
		&TestPlayerQuestEntity{},
	})
	if err != nil {
		t.Fatalf("OpenSession 失败: %v", err)
	}

	report.CacheGet1000 = measureLoop(1000, func() {
		_ = session.Get(&TestBatchFindEntity{})
	})
	if report.FindByIdOnce > 0 {
		perReadCache := report.CacheGet1000 / 1000
		if perReadCache == 0 {
			report.CacheSpeedup = 10000
		} else {
			report.CacheSpeedup = float64(report.FindByIdOnce) / float64(perReadCache)
		}
	}
	t.Logf("[读] Session.Get x1000=%v (约 %v/次, 相对 FindById 加速 %.0fx)",
		report.CacheGet1000, report.CacheGet1000/1000, report.CacheSpeedup)

	// 5. 并发 vs 串行多表
	loginTypes := []db233.IDbEntity{&TestBatchFindEntity{}, &TestPlayerBagEntity{}, &TestPlayerQuestEntity{}}
	report.Sequential3Find = measureLoop(5, func() {
		for _, et := range loginTypes {
			_, _ = repo.FindById(playerID, et)
		}
	}) / 5
	report.Concurrent3Find = measureLoop(5, func() {
		_ = repo.FindByIdConcurrent(playerID, loginTypes, nil)
	}) / 5
	t.Logf("[读] 3表串行=%v  3表并发=%v", report.Sequential3Find, report.Concurrent3Find)

	// 6. 批量写 vs 单条写
	batchEntities := make([]db233.IDbEntity, 50)
	for i := 0; i < 50; i++ {
		batchEntities[i] = &TestBatchFindEntity{
			PlayerID: fmt.Sprintf("%s_b%d", perfPlayerPrefix, i),
			Name:     "batch",
			Level:    i,
		}
	}
	report.BatchUpsert50 = measure(func() {
		if err := repo.UpdateBatchUpsert(batchEntities); err != nil {
			t.Fatalf("BatchUpsert 失败: %v", err)
		}
	})

	singleEntities := make([]db233.IDbEntity, 50)
	for i := 0; i < 50; i++ {
		singleEntities[i] = &TestBatchFindEntity{
			PlayerID: fmt.Sprintf("%s_s%d", perfPlayerPrefix, i),
			Name:     "single",
			Level:    i,
		}
	}
	report.SingleSave50 = measure(func() {
		for _, e := range singleEntities {
			if err := repo.Save(e); err != nil {
				t.Fatalf("Save 失败: %v", err)
			}
		}
	})
	t.Logf("[写] BatchUpsert50=%v  SingleSave50=%v", report.BatchUpsert50, report.SingleSave50)

	// 7. Session 延迟写 + 一次 Flush
	report.DeferredPut100 = measure(func() {
		cached := session.Get(&TestBatchFindEntity{})
		if cached == nil {
			t.Fatal("OpenSession 后应有缓存实体")
		}
		ent := cached.(*TestBatchFindEntity)
		for i := 0; i < 100; i++ {
			ent.Level = ent.Level + 1
			if err := session.Put(ent); err != nil {
				t.Fatalf("Put 失败: %v", err)
			}
		}
	})
	report.FlushOnce = measure(func() {
		if err := session.FlushDirtyOnly(); err != nil {
			t.Fatalf("FlushDirtyOnly 失败: %v", err)
		}
	})
	t.Logf("[写] 延迟 Put x100=%v  Flush一次=%v", report.DeferredPut100, report.FlushOnce)

	if err := sr.CloseSession(playerID); err != nil {
		t.Fatalf("CloseSession 主 Session 失败: %v", err)
	}

	// 8. Session 开闭循环稳定性
	report.SessionCycle20 = measure(func() {
		for i := 0; i < 20; i++ {
			pid := fmt.Sprintf("%s_cycle_%d", perfPlayerPrefix, i)
			seedPlayerData(t, repo, pid)
			s, err := sr.OpenSession(pid, loginTypes)
			if err != nil {
				t.Fatalf("OpenSession cycle 失败: %v", err)
			}
			if err := s.Put(&TestBatchFindEntity{PlayerID: pid, Name: "c", Level: 1}); err != nil {
				t.Fatalf("Put cycle 失败: %v", err)
			}
			if err := sr.CloseSession(pid); err != nil {
				t.Fatalf("CloseSession cycle 失败: %v", err)
			}
		}
	})
	if sr.OnlineCount() != 0 {
		t.Fatalf("Session 泄漏: 在线数=%d", sr.OnlineCount())
	}
	t.Logf("[稳定] Open/Close Session x20=%v", report.SessionCycle20)

	stats := db.DataSource.Stats()
	report.PoolInUse = stats.InUse
	report.PoolOpen = stats.OpenConnections
	t.Logf("[连接池] open=%d inUse=%d idle=%d wait=%d maxOpen=%d",
		stats.OpenConnections, stats.InUse, stats.Idle, stats.WaitCount, stats.MaxOpenConnections)

	report.Recommendations = buildPerfRecommendations(report)
	t.Log("\n========== 优化建议 ==========")
	for _, line := range report.Recommendations {
		t.Log("  • " + line)
	}

	// 稳定性断言（不依赖绝对延迟，避免本地 MySQL 计时分辨率不足误报）
	if report.FindByIdOnce < time.Microsecond || report.CacheGet1000/1000 == 0 {
		t.Logf("跳过 Session 缓存倍数断言: FindById=%v CachePerRead=%v，计时过短不足以稳定计算倍数",
			report.FindByIdOnce, report.CacheGet1000/1000)
	} else if report.CacheSpeedup < 5 {
		t.Errorf("Session 缓存加速不足 5x (实际 %.1fx)，请检查是否绕过 Session 读路径", report.CacheSpeedup)
	}
	if report.PingMax > 5*time.Second {
		t.Errorf("Ping 最大延迟过高: %v，请检查本地 MySQL 配置", report.PingMax)
	}
}

func measure(fn func()) time.Duration {
	start := time.Now()
	fn()
	return time.Since(start)
}

func measureLoop(n int, fn func()) time.Duration {
	start := time.Now()
	for i := 0; i < n; i++ {
		fn()
	}
	return time.Since(start)
}

func measurePingLatency(db *db233.Db, rounds int) (avg, max time.Duration) {
	var total time.Duration
	max = 0
	for i := 0; i < rounds; i++ {
		start := time.Now()
		if err := db.DataSource.Ping(); err != nil {
			continue
		}
		d := time.Since(start)
		total += d
		if d > max {
			max = d
		}
	}
	if rounds > 0 {
		avg = total / time.Duration(rounds)
	}
	return avg, max
}

func seedPlayerData(t *testing.T, repo *db233.BaseCrudRepository, playerID string) {
	t.Helper()
	entities := []db233.IDbEntity{
		&TestBatchFindEntity{PlayerID: playerID, Name: "perf", Level: 1},
		&TestPlayerBagEntity{PlayerID: playerID, Gold: 100},
		&TestPlayerQuestEntity{PlayerID: playerID, QuestData: "{}"},
	}
	for _, e := range entities {
		if err := repo.UpdateBatchUpsert([]db233.IDbEntity{e}); err != nil {
			t.Fatalf("seed 失败 playerID=%s type=%T: %v", playerID, e, err)
		}
	}
}

func cleanupPerfTables(db *db233.Db) {
	_, _ = db.DataSource.Exec("DELETE FROM test_batch_find WHERE playerId LIKE ?", perfPlayerPrefix+"%")
	_, _ = db.DataSource.Exec("DELETE FROM test_player_bag WHERE playerId LIKE ?", perfPlayerPrefix+"%")
	_, _ = db.DataSource.Exec("DELETE FROM test_player_quest WHERE playerId LIKE ?", perfPlayerPrefix+"%")
}

func buildPerfRecommendations(r *perfReport) []string {
	var lines []string

	if r.PingAvg > 20*time.Millisecond {
		lines = append(lines, fmt.Sprintf("MySQL Ping 均值 %v 偏高：确认本地 MySQL 负载与连接池配置；启动时 WarmConnectionPool 已启用", r.PingAvg))
	} else {
		lines = append(lines, fmt.Sprintf("MySQL Ping 均值 %v 正常", r.PingAvg))
	}

	if r.CacheSpeedup >= 50 {
		lines = append(lines, fmt.Sprintf("Session L1 缓存加速 %.0fx：在线读务必走 session.Get/GetOrLoad，勿直接 repo.FindById", r.CacheSpeedup))
	} else if r.CacheSpeedup >= 5 {
		lines = append(lines, fmt.Sprintf("Session 缓存加速 %.0fx：已有效，继续避免重复 FindById", r.CacheSpeedup))
	}

	if r.Concurrent3Find < r.Sequential3Find {
		ratio := float64(r.Sequential3Find) / float64(r.Concurrent3Find)
		lines = append(lines, fmt.Sprintf("FindByIdConcurrent 比串行快约 %.1fx：登录加载多表应使用 OpenSession/FindByIdConcurrent", ratio))
	} else {
		lines = append(lines, "并发加载未明显快于串行：表数量少或 RTT 低时可接受；35+ 表时收益更大")
	}

	if r.BatchUpsert50 > 0 && r.SingleSave50 > r.BatchUpsert50 {
		ratio := float64(r.SingleSave50) / float64(r.BatchUpsert50)
		lines = append(lines, fmt.Sprintf("BatchUpsert 比单条 Save 快约 %.1fx：关服/定时刷写已用 UpdateBatchUpsert", ratio))
	}

	if r.DeferredPut100 > 0 && r.FlushOnce > 0 {
		lines = append(lines, fmt.Sprintf("延迟写 Put x100=%v + Flush=%v：高频写开启 entityCache 减少 DB 压力", r.DeferredPut100, r.FlushOnce))
	}

	if r.PoolOpen > 0 && r.PoolInUse == 0 {
		lines = append(lines, "连接池空闲正常；maxOpenConns 可按单服 QPS 调整（推荐 50/10）")
	}

	if len(lines) == 0 {
		lines = append(lines, "各项指标正常")
	}
	return lines
}

// TestPerfStability_NoPanicWithoutDB 无 DB 时 Session 路径不 panic（稳定性回归）。
func TestPerfStability_NoPanicWithoutDB(t *testing.T) {
	db233.GetEntityCacheSettings().ApplyFull(db233.EntityCacheSettings{
		Enabled:                true,
		SessionFlushIntervalMs: 0,
	})
	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &TestBatchFindEntity{}})

	db := db233.NewDb(nil, 0, nil)
	sr := db233.NewSessionRepository(db233.NewBaseCrudRepository(db))
	defer sr.Stop()

	session, err := sr.OpenSession("nopanic", []db233.IDbEntity{&TestBatchFindEntity{}})
	if err != nil {
		// Load 可能返回连接错误，不应 panic
		if !strings.Contains(err.Error(), "panic") {
			return
		}
		t.Fatalf("不应 panic: %v", err)
	}
	_ = session.Get(&TestBatchFindEntity{})
	_, _ = session.GetOrLoad(&TestBatchFindEntity{})
	_ = session.Put(&TestBatchFindEntity{PlayerID: "nopanic", Name: "x", Level: 1})
	_ = session.FlushDirtyOnly()
	_ = sr.CloseSession("nopanic")
}
