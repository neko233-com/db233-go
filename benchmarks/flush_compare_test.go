package benchmarks

import (
	"fmt"
	"testing"
	"time"

	"github.com/neko233-com/db233-go/pkg/db233"
)

const (
	flushBenchSessions = 100
	flushBenchRuns     = 5
)

// TestFlushCompare_MergedVsPerSession 100 Session dirty 刷盘：合并 vs 逐 Session（需 MySQL）。
func TestFlushCompare_MergedVsPerSession(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过刷盘对比")
	}
	env := openBenchEnv(t)

	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &BenchPlayerEntity{}})
	SaveBenchEntityCache(t)

	mergedMs := benchFlushPath(t, env, true)
	perSessionMs := benchFlushPath(t, env, false)

	t.Log("\n========== Session 刷盘对比（100 在线 dirty，中位数 ms）==========")
	t.Logf("跨 Session 合并刷盘: %.3f ms", mergedMs)
	t.Logf("逐 Session 刷盘:     %.3f ms", perSessionMs)
	if perSessionMs > 0 {
		t.Logf("合并 / 逐 Session 比: %.2fx", mergedMs/perSessionMs)
	}

	if mergedMs > perSessionMs*1.05 {
		t.Errorf("合并刷盘 %.3fms 应不慢于逐 Session %.3fms", mergedMs, perSessionMs)
	}
}

func benchFlushPath(t *testing.T, env *benchEnv, mergeByTable bool) float64 {
	t.Helper()
	SetBenchEntityCacheKey(t, "sessionFlushIntervalMs", 0)
	SetBenchEntityCacheKey(t, "sessionFlushMergeByTable", mergeByTable)
	SetBenchEntityCacheKey(t, "sessionFlushMaxWorkers", 8)

	sr := db233.NewSessionRepository(env.Repo)
	defer sr.Stop()

	for i := 0; i < flushBenchSessions; i++ {
		pid := fmt.Sprintf("%sflush_%d", benchIDPrefix, i)
		s, err := sr.OpenSession(pid, []db233.IDbEntity{&BenchPlayerEntity{}})
		if err != nil {
			t.Fatalf("OpenSession: %v", err)
		}
		if err := s.Put(&BenchPlayerEntity{PlayerID: pid, Name: "flush", Level: i}); err != nil {
			t.Fatalf("Put: %s", db233.SafeErrorSummary(err))
		}
	}

	return benchMedian(t, flushBenchRuns, func() error {
		for i := 0; i < flushBenchSessions; i++ {
			pid := fmt.Sprintf("%sflush_%d", benchIDPrefix, i)
			s := sr.GetSession(pid)
			if s == nil {
				return fmt.Errorf("session missing: index=%d", i)
			}
			if err := s.Put(&BenchPlayerEntity{PlayerID: pid, Name: "flush", Level: i + 1}); err != nil {
				return err
			}
		}
		return sr.FlushAllDirty()
	})
}

// TestFlushCompare_Shutdown100Sessions 关服 FlushAll 100 Session（分波 + 合并）。
func TestFlushCompare_Shutdown100Sessions(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过关服刷盘压测")
	}
	env := openBenchEnv(t)

	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &BenchPlayerEntity{}})
	SaveBenchEntityCache(t)
	SetBenchEntityCacheKey(t, "sessionFlushIntervalMs", 0)
	SetBenchEntityCacheKey(t, "sessionFlushMergeByTable", true)
	SetBenchEntityCacheKey(t, "shutdownFlushMaxWorkers", 8)
	SetBenchEntityCacheKey(t, "shutdownFlushWaveIntervalMs", 10)

	sr := db233.NewSessionRepository(env.Repo)
	defer sr.Stop()

	for i := 0; i < flushBenchSessions; i++ {
		pid := fmt.Sprintf("%ssd_%d", benchIDPrefix, i)
		s, err := sr.OpenSession(pid, []db233.IDbEntity{&BenchPlayerEntity{}})
		if err != nil {
			t.Fatalf("OpenSession: %s", db233.SafeErrorSummary(err))
		}
		if err := s.Put(&BenchPlayerEntity{PlayerID: pid, Name: "sd", Level: 1}); err != nil {
			t.Fatalf("Put: %s", db233.SafeErrorSummary(err))
		}
	}

	start := time.Now()
	if err := sr.FlushAll(); err != nil {
		t.Fatalf("FlushAll: %v", err)
	}
	elapsed := time.Since(start)

	t.Logf("\n关服 FlushAll %d Session: %.3f ms", flushBenchSessions, float64(elapsed.Microseconds())/1000.0)

	got, err := env.Repo.FindById(fmt.Sprintf("%ssd_%d", benchIDPrefix, 50), &BenchPlayerEntity{})
	if err != nil || got == nil {
		t.Fatal("FlushAll 后数据应落库")
	}
}
