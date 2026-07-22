package benchmarks

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/neko233-com/db233-go/pkg/db233"
)

// TestStability_TrafficBurst 突发流量：多 goroutine 并发读/写/Session，框架不得 panic 或泄漏。
func TestStability_TrafficBurst(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过稳定性压测")
	}
	env := openBenchEnv(t)

	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &BenchPlayerEntity{}})
	SaveBenchEntityCache(t)
	SetBenchEntityCacheKey(t, "sessionFlushIntervalMs", 0)
	SetBenchEntityCacheKey(t, "maxSessions", 200)

	sr := db233.NewSessionRepository(env.Repo)
	defer sr.Stop()

	const (
		goroutines = 80
		opsPerG    = 15
	)
	var (
		errCount atomic.Int64
		wg       sync.WaitGroup
	)
	start := time.Now()

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			pid := fmt.Sprintf("%s_burst_%d", benchIDPrefix, id)
			if err := seedBenchPlayerEnv(env, pid); err != nil {
				errCount.Add(1)
				return
			}
			for i := 0; i < opsPerG; i++ {
				switch i % 5 {
				case 0, 1:
					_, err := env.Repo.FindById(pid, &BenchPlayerEntity{})
					if err != nil {
						errCount.Add(1)
					}
				case 2:
					session, err := sr.OpenSession(pid, []db233.IDbEntity{&BenchPlayerEntity{}})
					if err != nil {
						errCount.Add(1)
						continue
					}
					if session.Get(&BenchPlayerEntity{}) == nil {
						errCount.Add(1)
					}
				case 3:
					session := sr.GetSession(pid)
					if session == nil {
						errCount.Add(1)
						continue
					}
					ent, ok := session.Get(&BenchPlayerEntity{}).(*BenchPlayerEntity)
					if !ok || ent == nil {
						errCount.Add(1)
						continue
					}
					ent.Level++
					if err := session.Put(ent); err != nil {
						errCount.Add(1)
					}
				case 4:
					if err := sr.CloseSession(pid); err != nil {
						errCount.Add(1)
					}
				}
			}
		}(g)
	}
	wg.Wait()
	elapsed := time.Since(start)

	if errCount.Load() != 0 {
		t.Errorf("突发流量出现错误: %d", errCount.Load())
	}
	if sr.OnlineCount() > 50 {
		t.Errorf("Session 泄漏: online=%d", sr.OnlineCount())
	}
	if err := sr.FlushAll(); err != nil {
		t.Fatalf("FlushAll: %s", db233.SafeErrorSummary(err))
	}
	if err := env.SQL.Ping(); err != nil {
		t.Errorf("突发流量后连接池不可用: %v", err)
	}
	t.Logf("[稳定] 突发流量 %d goroutine x %d ops = %v, errors=%d, online=%d",
		goroutines, opsPerG, elapsed, errCount.Load(), sr.OnlineCount())
}

// TestStability_ConnectionPoolSpike 连接池尖峰后恢复。
func TestStability_ConnectionPoolSpike(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过")
	}
	env := openBenchEnv(t)

	env.SQL.SetMaxOpenConns(10)
	var (
		wg       sync.WaitGroup
		errCount atomic.Int64
	)
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if _, err := env.Repo.FindById(benchIDPrefix+"001", &BenchPlayerEntity{}); err != nil {
					errCount.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if errCount.Load() != 0 {
		t.Fatalf("连接池尖峰读失败: %d", errCount.Load())
	}
	time.Sleep(100 * time.Millisecond)
	if err := env.SQL.Ping(); err != nil {
		t.Fatalf("连接池尖峰后 Ping 失败: %v", err)
	}
	stats := env.SQL.Stats()
	t.Logf("[稳定] 尖峰后 pool open=%d inUse=%d idle=%d", stats.OpenConnections, stats.InUse, stats.Idle)
}

// TestStability_LRUBurst LRU 在 Session 洪峰下不崩溃且在线数受控。
func TestStability_LRUBurst(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过")
	}
	env := openBenchEnv(t)

	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &BenchPlayerEntity{}})
	SaveBenchEntityCache(t)
	SetBenchEntityCacheKey(t, "sessionFlushIntervalMs", 0)
	SetBenchEntityCacheKey(t, "maxSessions", 30)
	SetBenchEntityCacheKey(t, "flushOnEvict", false)

	sr := db233.NewSessionRepository(env.Repo)
	defer sr.Stop()

	for i := 0; i < 100; i++ {
		pid := fmt.Sprintf("%s_lru_%d", benchIDPrefix, i)
		if err := seedBenchPlayerEnv(env, pid); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		_, err := sr.OpenSession(pid, []db233.IDbEntity{&BenchPlayerEntity{}})
		if err != nil {
			t.Fatalf("OpenSession %d: %v", i, err)
		}
	}
	if c := sr.OnlineCount(); c > 35 {
		t.Errorf("LRU 未生效，在线=%d 期望 <=35", c)
	}
	if err := sr.FlushAll(); err != nil {
		t.Fatalf("FlushAll: %s", db233.SafeErrorSummary(err))
	}
	t.Logf("[稳定] LRU 洪峰后 online=%d", sr.OnlineCount())
}

// TestStability_WALBurst WAL + 批量写突发。
func TestStability_WALBurst(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过")
	}
	journalDir := t.TempDir()
	env := openBenchEnv(t)
	opts := db233.DefaultGameDbOptions()
	opts.EnableLocalJournal = true
	opts.EnableWriteBuffer = false
	opts.LocalJournalPath = journalDir
	opts.EntityTypes = []db233.IDbEntity{&BenchPlayerEntity{}}
	_, err := db233.InitGameDb(env.DB233, nil, opts)
	if err != nil {
		t.Fatalf("InitGameDb: %v", err)
	}
	repo := db233.NewBaseCrudRepository(env.DB233)

	var (
		wg       sync.WaitGroup
		errCount atomic.Int64
	)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ents := make([]db233.IDbEntity, 10)
			for j := 0; j < 10; j++ {
				ents[j] = &BenchPlayerEntity{
					PlayerID: fmt.Sprintf("%s_wal_%d_%d", benchIDPrefix, n, j),
					Name:     "w", Level: j,
				}
			}
			if err := repo.UpdateBatchUpsert(ents); err != nil {
				errCount.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if errCount.Load() != 0 {
		t.Fatalf("WAL 突发写失败: %d", errCount.Load())
	}
	if j := env.DB233.WriteJournal; j != nil {
		n, err := j.PendingCount()
		if err != nil {
			t.Fatalf("读取 WAL pending 失败: %s", db233.SafeErrorSummary(err))
		}
		if n > 0 {
			t.Errorf("WAL 不应有残留 pending=%d", n)
		}
	} else {
		t.Fatal("WAL 未初始化")
	}
	t.Log("[稳定] WAL 突发写完成，无 pending 残留")
}
