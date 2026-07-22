package db233

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestPreparedStmtCacheCoalescesConcurrentPrepareAndLeasesRetiredStmt(t *testing.T) {
	state := newScriptedDBState()
	datasource := openScriptedDB(t, state)
	cache := NewPreparedStmtCache(1, time.Hour)

	const workers = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			stmt, release, err := cache.acquireStmtContext(context.Background(), datasource, "UPDATE coalesced SET value = 1")
			if err == nil && stmt == nil {
				err = errors.New("stmt 为空")
			}
			if err == nil {
				time.Sleep(time.Millisecond)
				release()
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := cache.Len(); got != 1 {
		t.Fatalf("cache len=%d want=1", got)
	}
	if got := state.countCalls("prepare"); got != 1 {
		t.Fatalf("并发 miss 应合并为一次 Prepare，got=%d", got)
	}

	stmt, release, err := cache.acquireStmtContext(context.Background(), datasource, "UPDATE leased SET value = 1")
	if err != nil {
		t.Fatal(err)
	}
	_, releaseEvicted, err := cache.acquireStmtContext(context.Background(), datasource, "UPDATE evict SET value = 1")
	if err != nil {
		t.Fatal(err)
	}
	releaseEvicted()
	if stmt == nil {
		t.Fatal("淘汰时仍持 lease 的 stmt 不应被提前清空")
	}
	release()
}

func TestPreparedStmtCacheRemoveDBRetainsLeasedStmtUntilRelease(t *testing.T) {
	state := newScriptedDBState()
	datasource := openScriptedDB(t, state)
	cache := NewPreparedStmtCache(8, time.Hour)

	stmt, release, err := cache.acquireStmtContext(context.Background(), datasource, "UPDATE remove_db SET value = 1")
	if err != nil {
		t.Fatal(err)
	}
	cache.RemoveDB(datasource)
	if got := cache.Len(); got != 0 {
		t.Fatalf("RemoveDB 后 cache len=%d", got)
	}
	if stmt == nil {
		t.Fatal("lease 释放前 stmt 不应失效")
	}
	release()
}

func TestPreparedStmtCacheRemoveDBInvalidatesInFlightPrepare(t *testing.T) {
	entered := make(chan struct{}, 1)
	releasePrepare := make(chan struct{})
	state := newScriptedDBState()
	state.prepareEntered = entered
	state.prepareRelease = releasePrepare
	datasource := openScriptedDB(t, state)
	cache := NewPreparedStmtCache(8, time.Hour)

	result := make(chan error, 1)
	go func() {
		_, release, err := cache.acquireStmtContext(context.Background(), datasource, "UPDATE in_flight SET value = 1")
		if err == nil {
			release()
		}
		result <- err
	}()
	<-entered
	cache.RemoveDB(datasource)
	close(releasePrepare)
	if err := <-result; !errors.Is(err, errPreparedStmtCacheInvalidated) {
		t.Fatalf("RemoveDB 应使进行中的 Prepare 失效: %v", err)
	}
	if got := cache.Len(); got != 0 {
		t.Fatalf("失效 Prepare 不应写回缓存，len=%d", got)
	}
}

func TestPreparedStmtCacheGetStmtPinsSafelyAndRemainsBounded(t *testing.T) {
	state := newScriptedDBState()
	datasource := openScriptedDB(t, state)
	cache := NewPreparedStmtCache(1, time.Nanosecond)

	first, err := cache.GetStmt(datasource, "UPDATE pinned SET value = 1")
	if err != nil || first == nil {
		t.Fatalf("GetStmt pin 失败: stmt=%v err=%v", first, err)
	}
	time.Sleep(time.Millisecond)
	if _, _, err := cache.AcquireStmtContext(context.Background(), datasource, "UPDATE overflow SET value = 1"); !errors.Is(err, ErrPreparedStmtCacheFull) {
		t.Fatalf("全部条目固定时应 fail-closed，err=%v", err)
	}
	if got := cache.Len(); got != 1 {
		t.Fatalf("固定条目不应突破容量: len=%d", got)
	}
	cache.ConfigureFromSettings(CrudPerformanceSettings{StmtCacheSize: 1, StmtCacheTTLSeconds: 1})
	if got := cache.Len(); got != 1 {
		t.Fatalf("配置更新不应关闭固定条目: len=%d", got)
	}
	cache.RemoveDB(datasource)
	if got := cache.Len(); got != 0 {
		t.Fatalf("RemoveDB 必须清理固定条目: len=%d", got)
	}
}
