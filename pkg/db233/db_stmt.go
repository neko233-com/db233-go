package db233

import (
	"context"
	"database/sql"
)

// queryContext 执行查询；启用 Stmt 缓存时使用 Prepare。
func (db *Db) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if ctx == nil {
		return nil, NewValidationException("context 不能为 nil")
	}
	if ctxErr := contextCauseError(ctx); ctxErr != nil {
		return nil, NewQueryExceptionWithCause(ctxErr, "查询上下文已结束")
	}
	if db == nil || db.DataSource == nil {
		return nil, NewQueryException("数据库连接未初始化")
	}
	if db.closingState.Load() {
		return nil, ErrCrudRepositoryClosed
	}
	settings := GetCrudPerformanceSettings().Snapshot()
	if settings.EnablePreparedStmtCache {
		stmt, release, err := GetPreparedStmtCache().acquireStmtContext(ctx, db.DataSource, query)
		if err != nil {
			return nil, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "预编译查询失败")
		}
		defer release()
		rows, queryErr := stmt.QueryContext(ctx, args...)
		if queryErr != nil {
			return nil, NewQueryExceptionWithCause(joinErrorWithContext(queryErr, ctx), "查询执行失败: "+sqlForError(query))
		}
		return rows, nil
	}
	rows, err := db.DataSource.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "查询执行失败: "+sqlForError(query))
	}
	return rows, nil
}

// execContext 执行写操作；启用 Stmt 缓存时使用 Prepare。
func (db *Db) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.execContextWithFlushMetrics(ctx, "", 0, query, args...)
}

// execFlushContext 只在真正调用 database/sql 的 ExecContext 时记录一次 flush。
// Prepare、参数校验或资源状态在 Exec 前失败均不计数据库压力。
func (db *Db) execFlushContext(
	ctx context.Context,
	source FlushWriteSource,
	entityCount int,
	query string,
	args ...any,
) (sql.Result, error) {
	return db.execContextWithFlushMetrics(ctx, source, entityCount, query, args...)
}

func (db *Db) execContextWithFlushMetrics(
	ctx context.Context,
	source FlushWriteSource,
	entityCount int,
	query string,
	args ...any,
) (sql.Result, error) {
	if ctx == nil {
		return nil, NewValidationException("context 不能为 nil")
	}
	if ctxErr := contextCauseError(ctx); ctxErr != nil {
		return nil, NewQueryExceptionWithCause(ctxErr, "执行上下文已结束")
	}
	if db == nil || db.DataSource == nil {
		return nil, NewQueryException("数据库连接未初始化")
	}
	if db.closingState.Load() {
		return nil, ErrCrudRepositoryClosed
	}
	settings := GetCrudPerformanceSettings().Snapshot()
	if settings.EnablePreparedStmtCache {
		stmt, release, err := GetPreparedStmtCache().acquireStmtContext(ctx, db.DataSource, query)
		if err != nil {
			return nil, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "预编译执行失败")
		}
		defer release()
		if source != "" {
			db.recordFlushWriteAttempt(source, entityCount)
		}
		result, execErr := stmt.ExecContext(ctx, args...)
		if source != "" {
			db.recordFlushWriteResult(source, entityCount, execErr == nil)
		}
		if execErr != nil {
			return nil, NewQueryExceptionWithCause(joinErrorWithContext(execErr, ctx), "更新执行失败: "+sqlForError(query))
		}
		return result, nil
	}
	if source != "" {
		db.recordFlushWriteAttempt(source, entityCount)
	}
	result, err := db.DataSource.ExecContext(ctx, query, args...)
	if source != "" {
		db.recordFlushWriteResult(source, entityCount, err == nil)
	}
	if err != nil {
		return nil, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "更新执行失败: "+sqlForError(query))
	}
	return result, nil
}
