package tests

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/neko233-com/db233-go/pkg/db233"
)

const productionPlayerCount = 100
const productionUpdatesPerPlayer = 100

type productionPlayerEntity struct {
	PlayerID string `db:"playerId" primary_key:"true"`
	Score    int64  `db:"score"`
	Version  int64  `db:"version"`
}

func (*productionPlayerEntity) TableName() string       { return "test_player_concurrency_production" }
func (*productionPlayerEntity) SerializeBeforeSaveDb()  {}
func (*productionPlayerEntity) DeserializeAfterLoadDb() {}

// TestGameDb_100ConcurrentPlayersProductionPath 覆盖生产路径：WAL、Session、缓存、批量落库和完整关闭。
func TestGameDb_100ConcurrentPlayersProductionPath(t *testing.T) {
	savedPerformance := SaveCrudPerformanceSettings(t)
	savedEntityCache := SaveEntityCacheSettings(t)
	SaveCacheableEntityRegistry(t)

	performance := savedPerformance
	if performance.BatchUpsertChunkSize < productionPlayerCount {
		performance.BatchUpsertChunkSize = productionPlayerCount
	}
	db233.GetCrudPerformanceSettings().ApplyFull(performance)
	entityCache := savedEntityCache
	entityCache.Enabled = true
	entityCache.SessionFlushMergeByTable = true
	entityCache.SessionFlushIntervalMs = 10 * 60 * 1000
	if entityCache.MaxSessions < productionPlayerCount {
		entityCache.MaxSessions = productionPlayerCount
	}
	db233.GetEntityCacheSettings().ApplyFull(entityCache)

	db := CreateTestDb(t)
	if db == nil {
		return
	}
	baselineGoroutines := runtime.NumGoroutine()

	if _, err := db.DataSource.Exec(`
		CREATE TABLE IF NOT EXISTS test_player_concurrency_production (
			playerId VARCHAR(64) NOT NULL PRIMARY KEY,
			score BIGINT NOT NULL,
			version BIGINT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`); err != nil {
		_ = db.Close()
		t.Fatalf("准备并发玩家表失败: %v", err)
	}
	if _, err := db.DataSource.Exec("TRUNCATE TABLE test_player_concurrency_production"); err != nil {
		_ = db.Close()
		t.Fatalf("清空并发玩家表失败: %v", err)
	}

	opts := db233.DefaultGameDbOptions()
	opts.DatabaseGeneration = fmt.Sprintf("test-100-players-%d", time.Now().UnixNano())
	opts.LocalJournalPath = t.TempDir()
	opts.EnableLocalJournal = true
	opts.EnableWriteBuffer = true
	opts.EnableEntityCache = true
	opts.EntityTypes = []db233.IDbEntity{&productionPlayerEntity{}}
	opts.CacheableEntities = []db233.CacheableEntitySpec{{Prototype: &productionPlayerEntity{}}}

	dbConfig := db233.NewDefaultMySQLConfig("127.0.0.1", 3306, "root", "root", "db233_go")
	sessions, err := db233.InitGameDb(db, dbConfig, opts)
	if err != nil {
		_ = db.Close()
		t.Fatalf("初始化生产游戏 DB 失败: %v", err)
	}
	repo := db233.NewBaseCrudRepository(db)

	seed := make([]db233.IDbEntity, 0, productionPlayerCount)
	for i := 0; i < productionPlayerCount; i++ {
		seed = append(seed, &productionPlayerEntity{
			PlayerID: fmt.Sprintf("production-player-%03d", i),
			Score:    int64(i),
			Version:  1,
		})
	}
	if err := repo.UpdateBatchUpsert(seed); err != nil {
		_ = db.Close()
		t.Fatalf("批量初始化玩家失败: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, productionPlayerCount)
	playerIDs := make([]string, productionPlayerCount)
	var wg sync.WaitGroup
	for i := 0; i < productionPlayerCount; i++ {
		i := i
		playerIDs[i] = fmt.Sprintf("production-player-%03d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			playerID := playerIDs[i]
			session, openErr := sessions.OpenSession(playerID, []db233.IDbEntity{&productionPlayerEntity{}})
			if openErr != nil {
				errs <- fmt.Errorf("打开 %s: %w", playerID, openErr)
				return
			}
			entity, ok := session.Get(&productionPlayerEntity{}).(*productionPlayerEntity)
			if !ok || entity == nil {
				errs <- fmt.Errorf("%s 缺少已加载实体", playerID)
				return
			}
			for update := 1; update <= productionUpdatesPerPlayer; update++ {
				entity.Score = int64(i*productionUpdatesPerPlayer + update)
				entity.Version = int64(update + 1)
				if dirtyErr := session.MarkDirty(entity); dirtyErr != nil {
					errs <- fmt.Errorf("第 %d 次标脏 %s: %w", update, playerID, dirtyErr)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for workerErr := range errs {
		t.Error(workerErr)
	}
	if t.Failed() {
		_ = db.Close()
		return
	}

	if online := sessions.OnlineCount(); online != productionPlayerCount {
		_ = db.Close()
		t.Fatalf("统一刷写前在线 Session=%d, want %d", online, productionPlayerCount)
	}
	beforeFlush := db.FlushWriteMetrics()
	if err := sessions.FlushAllDirty(); err != nil {
		_ = db.Close()
		t.Fatalf("最终 Session 刷盘失败: %v", err)
	}
	afterFlush := db.FlushWriteMetrics()
	if attempted := afterFlush.AttemptedSQL - beforeFlush.AttemptedSQL; attempted != 1 {
		_ = db.Close()
		t.Fatalf("100 玩家合并 flush SQL=%d, want 1", attempted)
	}
	if succeeded := afterFlush.SucceededSQL - beforeFlush.SucceededSQL; succeeded != 1 {
		_ = db.Close()
		t.Fatalf("100 玩家成功 flush SQL=%d, want 1", succeeded)
	}
	if failed := afterFlush.FailedSQL - beforeFlush.FailedSQL; failed != 0 {
		_ = db.Close()
		t.Fatalf("100 玩家失败 flush SQL=%d, want 0", failed)
	}
	beforeSession := beforeFlush.BySource[db233.FlushWriteSourceSession]
	afterSession := afterFlush.BySource[db233.FlushWriteSourceSession]
	if attempted := afterSession.AttemptedSQL - beforeSession.AttemptedSQL; attempted != 1 {
		_ = db.Close()
		t.Fatalf("Session source flush SQL=%d, want 1", attempted)
	}
	if entities := afterSession.AttemptedEntities - beforeSession.AttemptedEntities; entities != productionPlayerCount {
		_ = db.Close()
		t.Fatalf("Session source flush entities=%d, want %d", entities, productionPlayerCount)
	}

	rows, err := db.DataSource.Query(`
		SELECT playerId, score, version
		FROM test_player_concurrency_production
		ORDER BY playerId
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("验证并发结果失败: %v", err)
	}
	count := 0
	for rows.Next() {
		var playerID string
		var score, version int64
		if scanErr := rows.Scan(&playerID, &score, &version); scanErr != nil {
			_ = rows.Close()
			_ = db.Close()
			t.Fatalf("读取并发结果失败: %v", scanErr)
		}
		wantPlayerID := playerIDs[count]
		wantScore := int64(count*productionUpdatesPerPlayer + productionUpdatesPerPlayer)
		wantVersion := int64(productionUpdatesPerPlayer + 1)
		if playerID != wantPlayerID || score != wantScore || version != wantVersion {
			_ = rows.Close()
			_ = db.Close()
			t.Fatalf(
				"最终状态不一致: row=%d player=%s score=%d version=%d, want player=%s score=%d version=%d",
				count,
				playerID,
				score,
				version,
				wantPlayerID,
				wantScore,
				wantVersion,
			)
		}
		count++
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		_ = rows.Close()
		_ = db.Close()
		t.Fatalf("遍历并发结果失败: %v", rowsErr)
	}
	if closeRowsErr := rows.Close(); closeRowsErr != nil {
		_ = db.Close()
		t.Fatalf("关闭并发结果集失败: %v", closeRowsErr)
	}
	if count != productionPlayerCount {
		_ = db.Close()
		t.Fatalf("并发结果数量=%d, want %d", count, productionPlayerCount)
	}
	if db.WriteJournal != nil {
		pending, pendingErr := db.WriteJournal.PendingCount()
		if pendingErr != nil || pending != 0 {
			_ = db.Close()
			t.Fatalf("WAL 未清空: pending=%d err=%v", pending, pendingErr)
		}
	}
	closeErrs := make(chan error, productionPlayerCount)
	var closeWG sync.WaitGroup
	for _, playerID := range playerIDs {
		playerID := playerID
		closeWG.Add(1)
		go func() {
			defer closeWG.Done()
			if closeErr := sessions.CloseSession(playerID); closeErr != nil {
				closeErrs <- fmt.Errorf("关闭 %s: %w", playerID, closeErr)
			}
		}()
	}
	closeWG.Wait()
	close(closeErrs)
	for closeErr := range closeErrs {
		t.Error(closeErr)
	}
	if t.Failed() {
		_ = db.Close()
		return
	}
	if sessions.OnlineCount() != 0 {
		_ = db.Close()
		t.Fatalf("Session 泄漏: %d", sessions.OnlineCount())
	}
	afterSessionClose := db.FlushWriteMetrics()
	if afterSessionClose.AttemptedSQL != afterFlush.AttemptedSQL {
		_ = db.Close()
		t.Fatalf(
			"无 dirty 的 CloseSession 产生额外 flush SQL: before=%d after=%d",
			afterFlush.AttemptedSQL,
			afterSessionClose.AttemptedSQL,
		)
	}

	if _, err := db.DataSource.Exec("DROP TABLE IF EXISTS test_player_concurrency_production"); err != nil {
		_ = db.Close()
		t.Fatalf("清理并发玩家表失败: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("完整关闭 DB 失败: %v", err)
	}
	afterDBClose := db.FlushWriteMetrics()
	if afterDBClose.AttemptedSQL != afterSessionClose.AttemptedSQL {
		t.Fatalf(
			"无 dirty 的 Db.Close 产生额外 flush SQL: before=%d after=%d",
			afterSessionClose.AttemptedSQL,
			afterDBClose.AttemptedSQL,
		)
	}

	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > baselineGoroutines+4 && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > baselineGoroutines+4 {
		t.Fatalf("后台 goroutine 疑似泄漏: baseline=%d got=%d", baselineGoroutines, got)
	}
}
