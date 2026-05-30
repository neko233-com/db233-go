package db233

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// JournalEntry 本地 WAL 条目（数据库不可用时保证数据不丢）。
type JournalEntry struct {
	ID             string    `json:"id"`
	Operation      string    `json:"operation"`
	TableName      string    `json:"tableName"`
	PrimaryKey     string    `json:"primaryKey"`
	EntityTypeName string    `json:"entityTypeName"`
	EntityJSON     []byte    `json:"entityJSON"`
	SQL            string    `json:"sql,omitempty"`
	Params         []any     `json:"params,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	RetryCount     int       `json:"retryCount"`
	LastError      string    `json:"lastError,omitempty"`
}

// LocalWriteJournal 本地预写日志：先落盘再写库，成功后删除；失败则无限重试。
type LocalWriteJournal struct {
	dir       string
	journalMu sync.Mutex
	replayMu  sync.Mutex

	stopCh   chan struct{}
	doneCh   chan struct{}
	repo     *BaseCrudRepository
	interval time.Duration
}

// NewLocalWriteJournal 创建本地 WAL。
func NewLocalWriteJournal(dir string, repo *BaseCrudRepository) *LocalWriteJournal {
	if dir == "" {
		dir = filepath.Join(".", ".db233_journal")
	}
	return &LocalWriteJournal{
		dir:      dir,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		repo:     repo,
		interval: 5 * time.Second,
	}
}

// SetRetryInterval 设置后台回放间隔。
func (j *LocalWriteJournal) SetRetryInterval(d time.Duration) {
	j.interval = d
}

// Start 启动后台回放协程。
func (j *LocalWriteJournal) Start() {
	go j.replayLoop()
	LogInfo("本地 WAL 已启动: dir=%s", j.dir)
}

// Stop 停止回放并 fsync 落盘。
func (j *LocalWriteJournal) Stop() {
	close(j.stopCh)
	<-j.doneCh
}

func (j *LocalWriteJournal) journalFile() string {
	return filepath.Join(j.dir, "pending.ndjson")
}

// AppendEntities 批量合并写入 WAL（同表同主键仅保留最新版本，一次 fsync）。
func (j *LocalWriteJournal) AppendEntities(operation string, entities []IDbEntity) ([]*JournalEntry, error) {
	if len(entities) == 0 {
		return nil, nil
	}
	if j.repo == nil {
		return nil, NewValidationException("WAL 未绑定 Repository")
	}

	j.journalMu.Lock()
	defer j.journalMu.Unlock()

	existing, err := j.readAllEntriesLocked()
	if err != nil {
		return nil, err
	}
	pending := j.dedupeEntries(existing)

	result := make([]*JournalEntry, 0, len(entities))
	cm := GetCrudManagerInstance()
	now := time.Now()

	for _, entity := range entities {
		if entity == nil {
			continue
		}
		tableName := j.repo.getTableName(entity)
		pk := cm.GetPrimaryKeyValue(entity)
		if j.repo.isZeroValue(pk) {
			return nil, NewValidationException(fmt.Sprintf("WAL 要求主键非零: 表=%s", tableName))
		}
		data, err := SerializeEntity(entity)
		if err != nil {
			return nil, err
		}
		pkStr := fmt.Sprintf("%v", pk)
		key := tableName + ":" + pkStr

		if old, ok := pending[key]; ok {
			old.Operation = operation
			old.EntityJSON = data
			old.EntityTypeName = EntityTypeName(entity)
			old.CreatedAt = now
			result = append(result, old)
			continue
		}

		entry := &JournalEntry{
			ID:             fmt.Sprintf("%d_%s_%s", now.UnixNano(), tableName, pkStr),
			Operation:      operation,
			TableName:      tableName,
			PrimaryKey:     pkStr,
			EntityTypeName: EntityTypeName(entity),
			EntityJSON:     data,
			CreatedAt:      now,
		}
		pending[key] = entry
		result = append(result, entry)
	}

	all := make([]*JournalEntry, 0, len(pending))
	for _, e := range pending {
		all = append(all, e)
	}
	if err := j.rewriteEntriesLocked(all); err != nil {
		return nil, err
	}
	return result, nil
}

func (j *LocalWriteJournal) dedupeEntries(entries []*JournalEntry) map[string]*JournalEntry {
	pending := make(map[string]*JournalEntry, len(entries))
	for _, e := range entries {
		key := e.TableName + ":" + e.PrimaryKey
		if old, ok := pending[key]; !ok || e.CreatedAt.After(old.CreatedAt) {
			pending[key] = e
		}
	}
	return pending
}

// RemoveEntries 写库成功后删除 WAL 条目。
func (j *LocalWriteJournal) RemoveEntries(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	removeSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		removeSet[id] = struct{}{}
	}

	j.journalMu.Lock()
	defer j.journalMu.Unlock()

	entries, err := j.readAllEntriesLocked()
	if err != nil {
		return err
	}
	remaining := make([]*JournalEntry, 0, len(entries))
	for _, e := range entries {
		if _, remove := removeSet[e.ID]; !remove {
			remaining = append(remaining, e)
		}
	}
	return j.rewriteEntriesLocked(remaining)
}

// PendingCount 返回待回放条目数。
func (j *LocalWriteJournal) PendingCount() (int, error) {
	j.journalMu.Lock()
	defer j.journalMu.Unlock()
	entries, err := j.readAllEntriesLocked()
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

// ReplayAll 回放所有 pending WAL（启动时或连接恢复后调用）。
func (j *LocalWriteJournal) ReplayAll() (success, failed int) {
	j.replayMu.Lock()
	defer j.replayMu.Unlock()

	j.journalMu.Lock()
	entries, err := j.readAllEntriesLocked()
	j.journalMu.Unlock()
	if err != nil || len(entries) == 0 {
		return 0, 0
	}
	if j.repo == nil || j.repo.db == nil || j.repo.db.DataSource == nil {
		LogDebug("WAL 回放跳过: 数据库未就绪, pending=%d", len(entries))
		return 0, len(entries)
	}

	entries = j.dedupeEntriesToSlice(entries)

	// 按表分组批量 UPSERT
	grouped := make(map[string][]IDbEntity)
	entryByTablePK := make(map[string]*JournalEntry)

	for _, entry := range entries {
		entity, err := DeserializeEntity(entry.EntityTypeName, entry.EntityJSON)
		if err != nil {
			LogError("WAL 反序列化失败: id=%s, err=%v", entry.ID, err)
			failed++
			continue
		}
		key := entry.TableName + ":" + entry.PrimaryKey
		grouped[entry.TableName] = append(grouped[entry.TableName], entity)
		entryByTablePK[key] = entry
	}

	var succeededIDs []string
	for _, entities := range grouped {
		if err := j.repo.saveBatchUpsertOnce(entities); err != nil {
			LogWarn("WAL 回放失败: 表=%s, 数量=%d, err=%v", j.repo.getTableName(entities[0]), len(entities), err)
			failed += len(entities)
			continue
		}
		for _, entity := range entities {
			tableName := j.repo.getTableName(entity)
			pk := fmt.Sprintf("%v", GetCrudManagerInstance().GetPrimaryKeyValue(entity))
			if entry, ok := entryByTablePK[tableName+":"+pk]; ok {
				succeededIDs = append(succeededIDs, entry.ID)
				success++
			}
		}
	}

	if len(succeededIDs) > 0 {
		if err := j.RemoveEntries(succeededIDs); err != nil {
			LogError("WAL 删除已成功条目失败: %v", err)
		}
	}
	if success > 0 {
		LogInfo("WAL 回放完成: 成功=%d, 失败=%d", success, failed)
	}
	return success, failed
}

func (j *LocalWriteJournal) replayLoop() {
	defer close(j.doneCh)
	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()

	// 启动立即回放一次
	j.ReplayAll()

	for {
		select {
		case <-j.stopCh:
			return
		case <-ticker.C:
			j.ReplayAll()
		}
	}
}

func (j *LocalWriteJournal) readAllEntriesLocked() ([]*JournalEntry, error) {
	path := j.journalFile()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []*JournalEntry{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return []*JournalEntry{}, nil
	}

	entries := make([]*JournalEntry, 0)
	lines := splitLines(data)
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var entry JournalEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			LogWarn("跳过损坏 WAL 行: %v", err)
			continue
		}
		entries = append(entries, &entry)
	}
	return entries, nil
}

func (j *LocalWriteJournal) rewriteEntriesLocked(entries []*JournalEntry) error {
	path := j.journalFile()
	tmpPath := path + ".tmp"

	if err := os.MkdirAll(j.dir, 0755); err != nil {
		return fmt.Errorf("创建 WAL 目录失败: %w", err)
	}

	if len(entries) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			f.Close()
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (j *LocalWriteJournal) dedupeEntriesToSlice(entries []*JournalEntry) []*JournalEntry {
	pending := j.dedupeEntries(entries)
	result := make([]*JournalEntry, 0, len(pending))
	for _, e := range pending {
		result = append(result, e)
	}
	return result
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
