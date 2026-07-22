package db233

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type walArchitectureStateA struct {
	PlayerID string `db:"playerId" primary_key:"true"`
	Revision int    `db:"revision"`
	Payload  string `db:"payload"`
}

func (*walArchitectureStateA) TableName() string       { return "wal_state_a" }
func (*walArchitectureStateA) SerializeBeforeSaveDb()  {}
func (*walArchitectureStateA) DeserializeAfterLoadDb() {}

type walArchitectureStateB struct {
	PlayerID string `db:"playerId" primary_key:"true"`
	Revision int    `db:"revision"`
	Payload  string `db:"payload"`
}

func (*walArchitectureStateB) TableName() string       { return "wal_state_b" }
func (*walArchitectureStateB) SerializeBeforeSaveDb()  {}
func (*walArchitectureStateB) DeserializeAfterLoadDb() {}

type walArchitectureStateC struct {
	PlayerID string `db:"playerId" primary_key:"true"`
	Revision int    `db:"revision"`
	Payload  string `db:"payload"`
}

func (*walArchitectureStateC) TableName() string       { return "wal_state_c" }
func (*walArchitectureStateC) SerializeBeforeSaveDb()  {}
func (*walArchitectureStateC) DeserializeAfterLoadDb() {}

func registerWALArchitectureEntities() {
	manager := GetCrudManagerInstance()
	registry := GetEntityTypeRegistry()
	for _, entity := range []IDbEntity{
		&walArchitectureStateA{},
		&walArchitectureStateB{},
		&walArchitectureStateC{},
	} {
		manager.AutoInitEntity(entity)
		registry.Register(entity)
	}
}

func walArchitectureBatch(players, revision int) []IDbEntity {
	entities := make([]IDbEntity, 0, players*3)
	for player := 0; player < players; player++ {
		entities = append(entities, walArchitecturePlayerBatch(player, revision)...)
	}
	return entities
}

func walArchitecturePlayerBatch(player, revision int) []IDbEntity {
	id := fmt.Sprintf("player-%05d", player)
	payload := fmt.Sprintf("state-%d", revision)
	return []IDbEntity{
		&walArchitectureStateA{PlayerID: id, Revision: revision, Payload: payload},
		&walArchitectureStateB{PlayerID: id, Revision: revision, Payload: payload},
		&walArchitectureStateC{PlayerID: id, Revision: revision, Payload: payload},
	}
}

func newWALArchitectureJournal(tb testing.TB, dir string) *LocalWriteJournal {
	tb.Helper()
	registerWALArchitectureEntities()
	journal := NewLocalWriteJournal(dir, NewBaseCrudRepository(nil))
	tb.Cleanup(func() {
		if err := journal.StopStrict(); err != nil {
			tb.Errorf("StopStrict: %v", err)
		}
	})
	return journal
}

func TestLocalWriteJournalRemainsComparable(t *testing.T) {
	_ = map[LocalWriteJournal]struct{}{}
}

func TestLocalWriteJournalMigratesLegacyEntryLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending.ndjson")
	payload, err := json.Marshal(&walArchitectureStateA{PlayerID: "legacy", Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	older := JournalEntry{
		ID:             "legacy-old",
		Operation:      "SaveBatchUpsert",
		TableName:      "wal_state_a",
		PrimaryKey:     "legacy",
		EntityTypeName: EntityTypeName(&walArchitectureStateA{}),
		EntityJSON:     payload,
		CreatedAt:      time.Unix(1, 0).UTC(),
	}
	newer := older
	newer.ID = "legacy-new"
	newer.CreatedAt = time.Unix(2, 0).UTC()
	var legacy bytes.Buffer
	for _, entry := range []JournalEntry{newer, older} {
		line, marshalErr := json.Marshal(entry)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		legacy.Write(line)
		legacy.WriteByte('\n')
	}
	if err := os.WriteFile(path, legacy.Bytes(), recoveryFileMode); err != nil {
		t.Fatal(err)
	}

	journal := newWALArchitectureJournal(t, dir)
	if count, err := journal.PendingCount(); err != nil || count != 1 {
		t.Fatalf("pending=%d err=%v", count, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if len(lines) != 1 {
		t.Fatalf("migrated records=%d, want 1", len(lines))
	}
	legacyLine, record, _, err := decodeJournalLogLine(lines[0])
	if err != nil || legacyLine || record.Kind != journalLogKindUpsert || record.Entry.ID != "legacy-new" {
		t.Fatalf("migration record=%+v legacy=%v err=%v", record, legacyLine, err)
	}
}

func TestLocalWriteJournalToleratesOnlyTornTail(t *testing.T) {
	t.Run("torn final upsert is discarded", func(t *testing.T) {
		dir := t.TempDir()
		journal := newWALArchitectureJournal(t, dir)
		if _, err := journal.AppendEntities("SaveBatchUpsert", walArchitectureBatch(1, 1)[:1]); err != nil {
			t.Fatal(err)
		}
		if err := journal.StopStrict(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "pending.ndjson")
		initialInfo, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, recoveryFileMode)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(`{"_db233WalVersion":1,"kind":"upsert","entry":`); err != nil {
			t.Fatal(err)
		}
		if err := f.Sync(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}

		reopened := newWALArchitectureJournal(t, dir)
		if count, err := reopened.PendingCount(); err != nil || count != 1 {
			t.Fatalf("pending=%d err=%v", count, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasSuffix(data, []byte{'\n'}) || int64(len(data)) != initialInfo.Size() {
			t.Fatalf("torn tail was not removed: size=%d want=%d", len(data), initialInfo.Size())
		}
	})

	t.Run("internal corruption is quarantined and blocked", func(t *testing.T) {
		dir := t.TempDir()
		journal := newWALArchitectureJournal(t, dir)
		if _, err := journal.AppendEntities("SaveBatchUpsert", walArchitectureBatch(1, 1)[:1]); err != nil {
			t.Fatal(err)
		}
		if err := journal.StopStrict(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "pending.ndjson")
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, recoveryFileMode)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString("{broken-record}\n"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}

		reopened := newWALArchitectureJournal(t, dir)
		if _, err := reopened.PendingCount(); !errors.Is(err, ErrDatabaseGenerationBlocked) {
			t.Fatalf("internal corruption err=%v", err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("corrupt WAL should be quarantined: %v", err)
		}
		quarantineEntries, err := os.ReadDir(filepath.Join(dir, "quarantine"))
		if err != nil || len(quarantineEntries) != 1 {
			t.Fatalf("quarantine entries=%d err=%v", len(quarantineEntries), err)
		}
	})

	t.Run("mixed legacy and versioned records are internal corruption", func(t *testing.T) {
		dir := t.TempDir()
		journal := newWALArchitectureJournal(t, dir)
		if _, err := journal.AppendEntities("SaveBatchUpsert", walArchitectureBatch(1, 1)[:1]); err != nil {
			t.Fatal(err)
		}
		if err := journal.StopStrict(); err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(&walArchitectureStateA{PlayerID: "legacy", Revision: 2})
		if err != nil {
			t.Fatal(err)
		}
		legacy, err := json.Marshal(JournalEntry{
			ID:             "mixed-legacy",
			Operation:      "SaveBatchUpsert",
			TableName:      "wal_state_a",
			PrimaryKey:     "legacy",
			EntityTypeName: EntityTypeName(&walArchitectureStateA{}),
			EntityJSON:     payload,
			CreatedAt:      time.Now().UTC(),
		})
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(filepath.Join(dir, "pending.ndjson"), os.O_APPEND|os.O_WRONLY, recoveryFileMode)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeAll(f, append(legacy, '\n')); err != nil {
			t.Fatal(closeFileWithError(f, err))
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}

		reopened := newWALArchitectureJournal(t, dir)
		if _, err := reopened.PendingCount(); !errors.Is(err, ErrDatabaseGenerationBlocked) {
			t.Fatalf("mixed WAL err=%v", err)
		}
	})
}

func TestLocalWriteJournalConditionalTombstoneCrashSemantics(t *testing.T) {
	t.Run("torn tombstone preserves entry", func(t *testing.T) {
		dir := t.TempDir()
		journal := newWALArchitectureJournal(t, dir)
		entries, err := journal.AppendEntities("SaveBatchUpsert", walArchitectureBatch(1, 1)[:1])
		if err != nil {
			t.Fatal(err)
		}
		if err := journal.StopStrict(); err != nil {
			t.Fatal(err)
		}
		line, err := encodeJournalLogRecord(journalLogRecord{
			FormatVersion: journalLogFormatVersion,
			Kind:          journalLogKindDelete,
			EntryID:       entries[0].ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(filepath.Join(dir, "pending.ndjson"), os.O_APPEND|os.O_WRONLY, recoveryFileMode)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(line[:len(line)/2]); err != nil {
			t.Fatal(err)
		}
		if err := f.Sync(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		reopened := newWALArchitectureJournal(t, dir)
		if count, err := reopened.PendingCount(); err != nil || count != 1 {
			t.Fatalf("pending=%d err=%v", count, err)
		}
	})

	t.Run("durable tombstone removes only matching version", func(t *testing.T) {
		dir := t.TempDir()
		journal := newWALArchitectureJournal(t, dir)
		oldEntries, err := journal.AppendEntities("SaveBatchUpsert", walArchitectureBatch(1, 1)[:1])
		if err != nil {
			t.Fatal(err)
		}
		newEntries, err := journal.AppendEntities("SaveBatchUpsert", walArchitectureBatch(1, 2)[:1])
		if err != nil {
			t.Fatal(err)
		}
		if err := journal.RemoveEntries([]string{oldEntries[0].ID}); err != nil {
			t.Fatal(err)
		}
		if count, err := journal.PendingCount(); err != nil || count != 1 {
			t.Fatalf("old tombstone removed newer entry: pending=%d err=%v", count, err)
		}
		if err := journal.StopStrict(); err != nil {
			t.Fatal(err)
		}
		line, err := encodeJournalLogRecord(journalLogRecord{
			FormatVersion: journalLogFormatVersion,
			Kind:          journalLogKindDelete,
			EntryID:       oldEntries[0].ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(filepath.Join(dir, "pending.ndjson"), os.O_APPEND|os.O_WRONLY, recoveryFileMode)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeAll(f, line); err != nil {
			t.Fatal(closeFileWithError(f, err))
		}
		if err := f.Sync(); err != nil {
			t.Fatal(closeFileWithError(f, err))
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}

		reopened := newWALArchitectureJournal(t, dir)
		reopened.journalMu.Lock()
		pending, err := reopened.readAllEntriesLocked()
		reopened.journalMu.Unlock()
		if err != nil || len(pending) != 1 || pending[0].ID != newEntries[0].ID {
			t.Fatalf("conditional tombstone pending=%+v err=%v", pending, err)
		}
		if err := reopened.RemoveEntries([]string{newEntries[0].ID}); err != nil {
			t.Fatal(err)
		}
		if count, err := reopened.PendingCount(); err != nil || count != 0 {
			t.Fatalf("matching tombstone pending=%d err=%v", count, err)
		}
	})
}

func TestLocalWriteJournalCompactsOnlyAfterRedundancyThreshold(t *testing.T) {
	dir := t.TempDir()
	journal := newWALArchitectureJournal(t, dir)
	entries, err := journal.AppendEntities("SaveBatchUpsert", walArchitectureBatch(100, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 300 {
		t.Fatalf("entries=%d, want 300", len(entries))
	}
	ids := make([]string, 260)
	for index := range ids {
		ids[index] = entries[index].ID
	}
	if err := journal.RemoveEntries(ids); err != nil {
		t.Fatal(err)
	}
	journal.journalMu.Lock()
	state := *journal.stateLocked()
	journal.journalMu.Unlock()
	if state.compactionCount != 1 || state.logRecords != 40 || len(state.pendingCache) != 40 {
		t.Fatalf("state after compaction: records=%d pending=%d compactions=%d", state.logRecords, len(state.pendingCache), state.compactionCount)
	}
	data, err := os.ReadFile(filepath.Join(dir, "pending.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := bytes.Count(data, []byte{'\n'}); lines != 40 {
		t.Fatalf("compacted lines=%d, want 40", lines)
	}
}

func TestLocalWriteJournalRecoversCompactionCrashArtifacts(t *testing.T) {
	t.Run("committed main wins over temporary snapshot", func(t *testing.T) {
		dir := t.TempDir()
		journal := newWALArchitectureJournal(t, dir)
		if _, err := journal.AppendEntities("SaveBatchUpsert", walArchitectureBatch(1, 1)[:1]); err != nil {
			t.Fatal(err)
		}
		if err := journal.StopStrict(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".pending.ndjson.compact.tmp"), []byte("partial"), recoveryFileMode); err != nil {
			t.Fatal(err)
		}

		reopened := newWALArchitectureJournal(t, dir)
		if count, err := reopened.PendingCount(); err != nil || count != 1 {
			t.Fatalf("pending=%d err=%v", count, err)
		}
		if _, err := os.Stat(filepath.Join(dir, ".pending.ndjson.compact.tmp")); !os.IsNotExist(err) {
			t.Fatalf("orphan temporary snapshot not removed: %v", err)
		}
	})

	t.Run("complete temporary snapshot is promoted when main is absent", func(t *testing.T) {
		dir := t.TempDir()
		payload, err := json.Marshal(&walArchitectureStateA{PlayerID: "recover", Revision: 7})
		if err != nil {
			t.Fatal(err)
		}
		entry := &JournalEntry{
			ID:             "compaction-recovery",
			Operation:      "SaveBatchUpsert",
			TableName:      "wal_state_a",
			PrimaryKey:     "recover",
			EntityTypeName: EntityTypeName(&walArchitectureStateA{}),
			EntityJSON:     payload,
			CreatedAt:      time.Now().UTC(),
		}
		line, err := encodeJournalLogRecord(journalLogRecord{
			FormatVersion: journalLogFormatVersion,
			Kind:          journalLogKindUpsert,
			Entry:         entry,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".pending.ndjson.compact.tmp"), line, recoveryFileMode); err != nil {
			t.Fatal(err)
		}

		journal := newWALArchitectureJournal(t, dir)
		if count, err := journal.PendingCount(); err != nil || count != 1 {
			t.Fatalf("promoted pending=%d err=%v", count, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "pending.ndjson")); err != nil {
			t.Fatalf("compaction snapshot was not promoted: %v", err)
		}
	})

	t.Run("legacy temporary snapshot is promoted then migrated", func(t *testing.T) {
		dir := t.TempDir()
		payload, err := json.Marshal(&walArchitectureStateA{PlayerID: "legacy-recover", Revision: 8})
		if err != nil {
			t.Fatal(err)
		}
		legacy, err := json.Marshal(JournalEntry{
			ID:             "legacy-compaction-recovery",
			Operation:      "SaveBatchUpsert",
			TableName:      "wal_state_a",
			PrimaryKey:     "legacy-recover",
			EntityTypeName: EntityTypeName(&walArchitectureStateA{}),
			EntityJSON:     payload,
			CreatedAt:      time.Now().UTC(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pending.ndjson.tmp"), append(legacy, '\n'), recoveryFileMode); err != nil {
			t.Fatal(err)
		}

		journal := newWALArchitectureJournal(t, dir)
		if count, err := journal.PendingCount(); err != nil || count != 1 {
			t.Fatalf("legacy promoted pending=%d err=%v", count, err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "pending.ndjson"))
		if err != nil {
			t.Fatal(err)
		}
		legacyLine, record, _, err := decodeJournalLogLine(bytes.TrimSpace(data))
		if err != nil || legacyLine || record.Kind != journalLogKindUpsert {
			t.Fatalf("legacy temp migration record=%+v legacy=%v err=%v", record, legacyLine, err)
		}
	})

	t.Run("incomplete temporary snapshot without main blocks", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".pending.ndjson.compact.tmp"), []byte("partial"), recoveryFileMode); err != nil {
			t.Fatal(err)
		}
		journal := newWALArchitectureJournal(t, dir)
		if _, err := journal.PendingCount(); !errors.Is(err, ErrDatabaseGenerationBlocked) {
			t.Fatalf("incomplete compaction err=%v", err)
		}
	})
}

func TestLocalWriteJournalHundredPlayersThreeTablesKeepsLatestState(t *testing.T) {
	dir := t.TempDir()
	journal := newWALArchitectureJournal(t, dir)
	started := time.Now()
	errorsCh := make(chan error, 100)
	var workers sync.WaitGroup
	for player := 0; player < 100; player++ {
		player := player
		workers.Add(1)
		go func() {
			defer workers.Done()
			for revision := 0; revision < 5; revision++ {
				if _, err := journal.AppendEntities("SaveBatchUpsert", walArchitecturePlayerBatch(player, revision)); err != nil {
					errorsCh <- fmt.Errorf("player=%d revision=%d: %w", player, revision, err)
					return
				}
			}
		}()
	}
	workers.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("100 concurrent players x 3 tables x 5 revisions persisted in %v", time.Since(started))
	if count, err := journal.PendingCount(); err != nil || count != 300 {
		t.Fatalf("pending=%d err=%v", count, err)
	}
	if err := journal.StopStrict(); err != nil {
		t.Fatal(err)
	}

	reopened := newWALArchitectureJournal(t, dir)
	reopened.journalMu.Lock()
	entries, err := reopened.readAllEntriesLocked()
	reopened.journalMu.Unlock()
	if err != nil || len(entries) != 300 {
		t.Fatalf("reopened entries=%d err=%v", len(entries), err)
	}
	for _, entry := range entries {
		var state struct {
			Revision int `json:"Revision"`
		}
		if err := json.Unmarshal(entry.EntityJSON, &state); err != nil {
			t.Fatal(err)
		}
		if state.Revision != 4 {
			t.Fatalf("table=%s key=%s revision=%d, want 4", entry.TableName, entry.PrimaryKey, state.Revision)
		}
	}
}

func TestLocalWriteJournalTenThousandPlayersDeltaIsAppendLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("10k WAL complexity test")
	}
	dir := t.TempDir()
	journal := newWALArchitectureJournal(t, dir)
	started := time.Now()
	if _, err := journal.AppendEntities("SaveBatchUpsert", walArchitectureBatch(10_000, 1)); err != nil {
		t.Fatal(err)
	}
	journal.journalMu.Lock()
	initialBytes := journal.stateLocked().appendedBytes
	initialCompactions := journal.stateLocked().compactionCount
	journal.journalMu.Unlock()
	initialInfo, err := os.Stat(filepath.Join(dir, "pending.ndjson"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := journal.AppendEntities("SaveBatchUpsert", walArchitectureBatch(100, 2)); err != nil {
		t.Fatal(err)
	}
	journal.journalMu.Lock()
	state := *journal.stateLocked()
	journal.journalMu.Unlock()
	deltaBytes := state.appendedBytes - initialBytes
	finalInfo, err := os.Stat(filepath.Join(dir, "pending.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if state.compactionCount != initialCompactions {
		t.Fatalf("small delta triggered full compaction: before=%d after=%d", initialCompactions, state.compactionCount)
	}
	if deltaBytes == 0 || deltaBytes*20 >= initialBytes {
		t.Fatalf("delta append not O(delta): initial=%d delta=%d", initialBytes, deltaBytes)
	}
	if growth := uint64(finalInfo.Size() - initialInfo.Size()); growth != deltaBytes {
		t.Fatalf("file growth=%d appended delta=%d", growth, deltaBytes)
	}
	if count, err := journal.PendingCount(); err != nil || count != 30_000 {
		t.Fatalf("pending=%d err=%v", count, err)
	}
	elapsed := time.Since(started)
	t.Logf("30k live records + 300-record delta: initial=%dB delta=%dB elapsed=%v", initialBytes, deltaBytes, elapsed)
	if elapsed > 30*time.Second {
		t.Fatalf("10k-player multi-table WAL path took %v", elapsed)
	}
}

func BenchmarkLocalWriteJournalAppendDelta100PlayersThreeTables(b *testing.B) {
	journal := newWALArchitectureJournal(b, b.TempDir())
	if _, err := journal.AppendEntities("SaveBatchUpsert", walArchitectureBatch(10_000, 0)); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, err := journal.AppendEntities("SaveBatchUpsert", walArchitectureBatch(100, iteration+1)); err != nil {
			b.Fatal(err)
		}
	}
}
