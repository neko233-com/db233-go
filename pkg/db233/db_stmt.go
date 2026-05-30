package db233

import (
	"context"
	"database/sql"
)

// queryContext 执行查询；启用 Stmt 缓存时使用 Prepare。
func (db *Db) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if db == nil || db.DataSource == nil {
		return nil, NewQueryException("数据库连接未初始化")
	}
	settings := GetCrudPerformanceSettings().Snapshot()
	if settings.EnablePreparedStmtCache {
		if stmt, err := GetPreparedStmtCache().GetStmt(db.DataSource, query); err == nil {
			return stmt.QueryContext(ctx, args...)
		}
	}
	return db.DataSource.QueryContext(ctx, query, args...)
}

// execContext 执行写操作；启用 Stmt 缓存时使用 Prepare。
func (db *Db) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if db == nil || db.DataSource == nil {
		return nil, NewQueryException("数据库连接未初始化")
	}
	settings := GetCrudPerformanceSettings().Snapshot()
	if settings.EnablePreparedStmtCache {
		if stmt, err := GetPreparedStmtCache().GetStmt(db.DataSource, query); err == nil {
			return stmt.ExecContext(ctx, args...)
		}
	}
	return db.DataSource.ExecContext(ctx, query, args...)
}
