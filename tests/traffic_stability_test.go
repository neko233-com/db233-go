package tests

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/neko233-com/db233-go/pkg/db233"
)

// TestTrafficBurst_Stability 主测试包突发流量稳定性（与 benchmarks 互补）。
func TestTrafficBurst_Stability(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过")
	}
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()
	if err := setupBatchFindTable(db); err != nil {
		t.Fatal(err)
	}
	defer db.DataSource.Exec("DELETE FROM test_batch_find WHERE playerId LIKE 'traffic_%'")

	db233.RegisterDbForConnectionPool(db)
	SaveEntityCacheSettings(t)
	SetEntityCacheKey(t, "sessionFlushIntervalMs", 0)
	SetEntityCacheKey(t, "maxSessions", 100)
	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &TestBatchFindEntity{}})

	repo := db233.NewBaseCrudRepository(db)
	sr := db233.NewSessionRepository(repo)
	defer sr.Stop()

	const workers = 50
	var errs atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			pid := fmt.Sprintf("traffic_%d", id)
			_ = repo.UpdateBatchUpsert([]db233.IDbEntity{
				&TestBatchFindEntity{PlayerID: pid, Name: "t", Level: 1},
			})
			for i := 0; i < 20; i++ {
				if i%4 == 0 {
					s, err := sr.OpenSession(pid, []db233.IDbEntity{&TestBatchFindEntity{}})
					if err != nil {
						errs.Add(1)
						continue
					}
					_ = s.Put(&TestBatchFindEntity{PlayerID: pid, Name: "t", Level: i})
				} else if i%4 == 3 {
					_ = sr.CloseSession(pid)
				} else {
					_, _ = repo.FindById(pid, &TestBatchFindEntity{})
				}
			}
		}(w)
	}
	wg.Wait()
	_ = sr.FlushAll()
	if errs.Load() > workers {
		t.Errorf("突发错误过多: %d", errs.Load())
	}
	if err := db.DataSource.Ping(); err != nil {
		t.Fatalf("连接池恢复失败: %v", err)
	}
	t.Logf("[稳定] traffic burst workers=%d errors=%d online=%d", workers, errs.Load(), sr.OnlineCount())
}

// TestTrafficBurst_PoolRecovery 连接池抖动恢复。
func TestTrafficBurst_PoolRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过")
	}
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()
	if err := setupBatchFindTable(db); err != nil {
		t.Fatal(err)
	}
	_, _ = db.DataSource.Exec("INSERT INTO test_batch_find (playerId,name,level) VALUES ('pool_ping', 'p', 1) ON DUPLICATE KEY UPDATE name=VALUES(name)")

	db.DataSource.SetMaxOpenConns(8)
	repo := db233.NewBaseCrudRepository(db)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 8; j++ {
				_, _ = repo.FindById("pool_ping", &TestBatchFindEntity{})
				time.Sleep(time.Millisecond)
			}
		}()
	}
	wg.Wait()
	time.Sleep(200 * time.Millisecond)
	if err := db.DataSource.Ping(); err != nil {
		t.Fatalf("池恢复失败: %v", err)
	}
}
