package db233

import (
	"context"
	"time"
)

const contextLockPollInterval = time.Millisecond

// lockCurrentDatabaseGenerationContext is the cancellable startup/read-side
// generation lease. Runtime hot paths keep the blocking RLock implementation;
// startup paths need cancellation so a queued clear/close cannot make their
// deadline ineffective before database I/O starts.
func (db *Db) lockCurrentDatabaseGenerationContext(ctx context.Context) (string, func(), error) {
	if ctx == nil {
		return "", func() {}, NewValidationException("DatabaseGeneration context 不能为 nil")
	}
	if db == nil {
		return "", func() {}, nil
	}
	if err := ctx.Err(); err != nil {
		return "", func() {}, err
	}
	if db.generationUnavailable.Load() {
		return "", func() {}, ErrDatabaseGenerationBlocked
	}
	if !db.generationMu.TryRLock() {
		ticker := time.NewTicker(contextLockPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return "", func() {}, context.Cause(ctx)
			case <-ticker.C:
				if db.generationUnavailable.Load() {
					return "", func() {}, ErrDatabaseGenerationBlocked
				}
				if db.generationMu.TryRLock() {
					goto locked
				}
			}
		}
	}

locked:
	if err := ctx.Err(); err != nil {
		db.generationMu.RUnlock()
		return "", func() {}, err
	}
	if db.generationUnavailable.Load() {
		db.generationMu.RUnlock()
		return "", func() {}, ErrDatabaseGenerationBlocked
	}
	if db.generationErr != nil {
		err := db.generationErr
		db.generationMu.RUnlock()
		return "", func() {}, err
	}
	return db.databaseGeneration, db.generationMu.RUnlock, nil
}
