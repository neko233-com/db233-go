package db233

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func setupWriteBufferTest(t *testing.T) (*BaseCrudRepository, *WriteBuffer) {
	t.Helper()
	repo := NewBaseCrudRepository(nil)
	GetCrudManagerInstance().AutoInitEntity(&flushTestEntity{})
	GetCrudManagerInstance().AutoInitEntity(&flushTestEntityB{})
	wb := newWriteBuffer(repo)
	return repo, wb
}

func TestWriteBuffer_EnqueueDedupesByPK(t *testing.T) {
	_, wb := setupWriteBufferTest(t)
	e1 := &flushTestEntity{PlayerID: "same", Name: "v1", Level: 1}
	e2 := &flushTestEntity{PlayerID: "same", Name: "v2", Level: 2}

	queued, err := wb.Enqueue(e1)
	if err != nil || !queued {
		t.Fatalf("enqueue1: queued=%v err=%v", queued, err)
	}
	queued, err = wb.Enqueue(e2)
	if err != nil || !queued {
		t.Fatalf("enqueue2: queued=%v err=%v", queued, err)
	}
	if wb.queueSize() != 1 {
		t.Fatalf("queue size=%d want 1", wb.queueSize())
	}
}

func TestWriteBuffer_FlushRespectsMaxBatchSize(t *testing.T) {
	repo, wb := setupWriteBufferTest(t)
	rec := newUpsertRecorder()
	repo.SetTestUpsertHook(rec.hook)

	mgr := GetCrudPerformanceSettings()
	saved := mgr.Snapshot()
	t.Cleanup(func() { mgr.ApplyFull(saved) })
	mgr.ApplyFull(CrudPerformanceSettings{
		WriteBufferMaxBatchSize: 3,
		BatchUpsertChunkSize:    200,
	})

	for i := 0; i < 7; i++ {
		_, _ = wb.Enqueue(&flushTestEntity{PlayerID: fmt.Sprintf("wb%d", i), Name: "n", Level: i})
	}
	if err := wb.Flush(); err != nil {
		t.Fatal(err)
	}
	if rec.batchCount() != 3 { // 3+3+1
		t.Fatalf("batch count=%d want 3", rec.batchCount())
	}
	if wb.queueSize() != 0 {
		t.Fatalf("queue not empty after flush")
	}
}

func TestWriteBuffer_FlushMultiTable(t *testing.T) {
	repo, wb := setupWriteBufferTest(t)
	rec := newUpsertRecorder()
	repo.SetTestUpsertHook(rec.hook)

	_, _ = wb.Enqueue(&flushTestEntity{PlayerID: "a", Name: "n"})
	_, _ = wb.Enqueue(&flushTestEntityB{PlayerID: "b", Gold: 9})
	if err := wb.Flush(); err != nil {
		t.Fatal(err)
	}
	if rec.batchCount() != 2 {
		t.Fatalf("batch count=%d want 2 tables", rec.batchCount())
	}
}

func TestWriteBuffer_EnqueueQueueFullReturnsFalse(t *testing.T) {
	_, wb := setupWriteBufferTest(t)
	mgr := GetCrudPerformanceSettings()
	saved := mgr.Snapshot()
	t.Cleanup(func() { mgr.ApplyFull(saved) })
	mgr.ApplyFull(CrudPerformanceSettings{WriteBufferMaxQueueSize: 2})

	_, _ = wb.Enqueue(&flushTestEntity{PlayerID: "a", Name: "n"})
	_, _ = wb.Enqueue(&flushTestEntity{PlayerID: "b", Name: "n"})
	queued, err := wb.Enqueue(&flushTestEntity{PlayerID: "c", Name: "n"})
	if err != nil {
		t.Fatal(err)
	}
	if queued {
		t.Fatal("queue full should return queued=false")
	}
}

func TestWriteBuffer_FlushRestoresPendingOnError(t *testing.T) {
	repo, wb := setupWriteBufferTest(t)
	rec := newUpsertRecorder()
	rec.failOnCall = 1
	repo.SetTestUpsertHook(rec.hook)

	_, _ = wb.Enqueue(&flushTestEntity{PlayerID: "r1", Name: "n"})
	if err := wb.Flush(); err == nil {
		t.Fatal("expected flush error")
	}
	if wb.queueSize() != 1 {
		t.Fatalf("pending should restore, size=%d", wb.queueSize())
	}
}

func TestWriteBuffer_StopFlushesPending(t *testing.T) {
	repo, wb := setupWriteBufferTest(t)
	rec := newUpsertRecorder()
	repo.SetTestUpsertHook(rec.hook)

	mgr := GetCrudPerformanceSettings()
	saved := mgr.Snapshot()
	t.Cleanup(func() { mgr.ApplyFull(saved) })
	mgr.ApplyFull(CrudPerformanceSettings{WriteBufferFlushIntervalMs: 100})

	wb.Start(saved)
	_, _ = wb.Enqueue(&flushTestEntity{PlayerID: "stop1", Name: "n"})
	time.Sleep(10 * time.Millisecond)
	if err := wb.Stop(); err != nil {
		t.Fatal(err)
	}
	if rec.batchCount() == 0 {
		t.Fatal("Stop should flush pending")
	}
}

func TestWriteBuffer_LoopSingleFlusher(t *testing.T) {
	repo, wb := setupWriteBufferTest(t)
	var mu sync.Mutex
	inFlight := 0
	maxInFlight := 0
	repo.SetTestUpsertHook(func(entities []IDbEntity) error {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		return nil
	})

	mgr := GetCrudPerformanceSettings()
	saved := mgr.Snapshot()
	t.Cleanup(func() { mgr.ApplyFull(saved) })
	cfg := saved
	cfg.WriteBufferFlushIntervalMs = 15
	mgr.ApplyFull(cfg)

	wb.Start(cfg)
	for i := 0; i < 5; i++ {
		_, _ = wb.Enqueue(&flushTestEntity{PlayerID: fmt.Sprintf("lp%d", i), Name: "n"})
	}
	time.Sleep(80 * time.Millisecond)
	_ = wb.Stop()
	if maxInFlight > 1 {
		t.Fatalf("write buffer should single-flush, maxInFlight=%d", maxInFlight)
	}
}

func TestWriteBuffer_EnqueueValidation(t *testing.T) {
	_, wb := setupWriteBufferTest(t)
	if _, err := wb.Enqueue(nil); err == nil {
		t.Fatal("nil entity")
	}
	if _, err := wb.Enqueue(&flushTestEntity{PlayerID: "", Name: "x"}); err == nil {
		t.Fatal("zero pk")
	}
}

func TestWriteBuffer_StopIdempotentConcurrent(t *testing.T) {
	repo, wb := setupWriteBufferTest(t)
	recorder := newUpsertRecorder()
	repo.SetTestUpsertHook(recorder.hook)

	settings := DefaultCrudPerformanceSettings()
	settings.WriteBufferFlushIntervalMs = int(time.Hour / time.Millisecond)
	wb.Start(settings)
	if queued, err := wb.Enqueue(&flushTestEntity{PlayerID: "stop-many", Name: "latest"}); err != nil || !queued {
		t.Fatalf("enqueue: queued=%v err=%v", queued, err)
	}

	const workers = 32
	var wg sync.WaitGroup
	errorsCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errorsCh <- wb.Stop()
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	}
	if recorder.batchCount() != 1 {
		t.Fatalf("Stop 应只完成一次最终刷写: batches=%d", recorder.batchCount())
	}
	if _, err := wb.Enqueue(&flushTestEntity{PlayerID: "after-stop"}); !errors.Is(err, ErrWriteBufferStopped) {
		t.Fatalf("停止后 Enqueue error=%v, want ErrWriteBufferStopped", err)
	}
}

func TestWriteBuffer_StopBeforeStart(t *testing.T) {
	_, wb := setupWriteBufferTest(t)
	if err := wb.Stop(); err != nil {
		t.Fatal(err)
	}
	wb.Start(DefaultCrudPerformanceSettings())
	if _, err := wb.Enqueue(&flushTestEntity{PlayerID: "late"}); !errors.Is(err, ErrWriteBufferStopped) {
		t.Fatalf("Stop-before-Start 后仍可入队: %v", err)
	}
}

func TestWriteBuffer_FailedFlushDoesNotOverwriteNewerEnqueue(t *testing.T) {
	repo, wb := setupWriteBufferTest(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	repo.SetTestUpsertHook(func([]IDbEntity) error {
		close(entered)
		<-release
		return errors.New("injected flush failure")
	})

	_, _ = wb.Enqueue(&flushTestEntity{PlayerID: "same", Name: "old", Level: 1})
	flushErr := make(chan error, 1)
	go func() { flushErr <- wb.Flush() }()
	<-entered
	_, _ = wb.Enqueue(&flushTestEntity{PlayerID: "same", Name: "new", Level: 2})
	close(release)
	if err := <-flushErr; err == nil {
		t.Fatal("expected injected failure")
	}

	wb.mu.Lock()
	entity := wb.pending["flush_test_entity"]["same"]
	wb.mu.Unlock()
	got, ok := entity.(*flushTestEntity)
	if !ok || got.Name != "new" || got.Level != 2 {
		t.Fatalf("失败回滚覆盖了新值: %#v", entity)
	}
}

func TestWriteBuffer_RetryBackoffIsBoundedAndResets(t *testing.T) {
	base := 100 * time.Millisecond
	for failures, want := range map[uint]time.Duration{
		0: base,
		1: 200 * time.Millisecond,
		2: 400 * time.Millisecond,
		3: 800 * time.Millisecond,
		8: maxWriteBufferRetryInterval,
	} {
		if got := writeBufferRetryInterval(base, failures); got != want {
			t.Fatalf("failures=%d retry=%v, want %v", failures, got, want)
		}
	}
	if got := writeBufferRetryInterval(4*time.Second, 1); got != maxWriteBufferRetryInterval {
		t.Fatalf("large base retry=%v, want cap %v", got, maxWriteBufferRetryInterval)
	}
}
