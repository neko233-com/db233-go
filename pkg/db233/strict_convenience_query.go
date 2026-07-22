package db233

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"
)

// DefaultStrictQueryTimeout bounds strict convenience wrappers without an
// explicit context. Long-running queries should use their Context variants.
const DefaultStrictQueryTimeout = 30 * time.Second

func strictQueryContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), DefaultStrictQueryTimeout)
}

// QueryStrict executes a raw query with all-or-error semantics.
func (db *Db) QueryStrict(query string, params ...any) ([]map[string]any, error) {
	ctx, cancel := strictQueryContext()
	defer cancel()
	return db.QueryStrictContext(ctx, query, params...)
}

// QueryStrictContext returns no partial rows when Query, Columns, Scan,
// iteration, cancellation, or Close fails.
func (db *Db) QueryStrictContext(ctx context.Context, query string, params ...any) (results []map[string]any, resultErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			results = nil
			panicCause, ok := recovered.(error)
			if !ok {
				panicCause = fmt.Errorf("raw query panic: %s", safeValueForLog(recovered))
			}
			resultErr = errors.Join(resultErr, NewQueryExceptionWithCause(panicCause, "raw query panic: "+sqlForError(query)))
		}
	}()
	if ctx == nil {
		return nil, NewValidationException("raw query context 不能为 nil")
	}
	if db == nil || db.DataSource == nil {
		return nil, NewQueryException("数据库连接未初始化")
	}
	rows, err := db.queryContext(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			results = nil
			resultErr = errors.Join(resultErr, NewQueryExceptionWithCause(closeErr, "关闭 raw query 结果集失败"))
		}
	}()
	columns, err := rows.Columns()
	if err != nil {
		return nil, NewQueryExceptionWithCause(err, "读取 raw query 列失败")
	}
	if len(columns) == 0 {
		return nil, NewQueryException("raw query 没有列")
	}
	results = make([]map[string]any, 0)
	for rowIndex := 0; rows.Next(); rowIndex++ {
		row, scanErr := scanRowsToMaps(columns, func(dest []any) error { return rows.Scan(dest...) })
		if scanErr != nil {
			return nil, NewQueryExceptionWithCause(scanErr, fmt.Sprintf("raw query 扫描失败: row=%d", rowIndex))
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "raw query 行遍历失败")
	}
	return results, nil
}

// ExecuteSqlByStatementStrictContext is the strict counterpart of the legacy
// partial-result ExecuteSqlByStatement API.
func (db *Db) ExecuteSqlByStatementStrictContext(ctx context.Context, statement *SqlStatement) ([]map[string]any, error) {
	if statement == nil {
		return nil, NewValidationException("SqlStatement 不能为 nil")
	}
	if !statement.IsQuery {
		return nil, NewValidationException("SqlStatement 不是查询")
	}
	results := make([]map[string]any, 0)
	for index, query := range statement.SqlList {
		batch, err := db.QueryStrictContext(ctx, query)
		if err != nil {
			return nil, NewQueryExceptionWithCause(err, fmt.Sprintf("执行 SqlStatement 查询失败: index=%d", index))
		}
		results = append(results, batch...)
	}
	return results, nil
}

func (db *Db) ExecuteSqlByStatementStrict(statement *SqlStatement) ([]map[string]any, error) {
	ctx, cancel := strictQueryContext()
	defer cancel()
	return db.ExecuteSqlByStatementStrictContext(ctx, statement)
}

func (db *Db) QueryNamedStrictContext(ctx context.Context, query string, params map[string]any) ([]map[string]any, error) {
	boundQuery, values, err := replaceSqlNamedParameters(query, params)
	if err != nil {
		return nil, err
	}
	return db.QueryStrictContext(ctx, boundQuery, values...)
}

func (db *Db) QueryNamedStrict(query string, params map[string]any) ([]map[string]any, error) {
	ctx, cancel := strictQueryContext()
	defer cancel()
	return db.QueryNamedStrictContext(ctx, query, params)
}

func queryFirstColumnStrictContext[T any](
	db *Db,
	ctx context.Context,
	query string,
	allRows bool,
	params ...any,
) (results []T, resultErr error) {
	if ctx == nil {
		return nil, NewValidationException("标量查询 context 不能为 nil")
	}
	if db == nil || db.DataSource == nil {
		return nil, NewQueryException("数据库连接未初始化")
	}
	rows, err := db.queryContext(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			results = nil
			resultErr = errors.Join(resultErr, NewQueryExceptionWithCause(closeErr, "关闭标量查询结果集失败"))
		}
	}()
	columns, err := rows.Columns()
	if err != nil {
		return nil, NewQueryExceptionWithCause(err, "读取标量查询列失败")
	}
	if len(columns) == 0 {
		return nil, NewQueryException("标量查询没有列")
	}
	results = make([]T, 0)
	targetType := reflect.TypeOf(*new(T))
	for rowIndex := 0; rows.Next(); rowIndex++ {
		scratch := acquireScanScratch(len(columns))
		for index := range scratch.dest {
			scratch.dest[index] = scratch.discardPtr(index)
		}
		scanErr := rows.Scan(scratch.dest...)
		if scanErr != nil {
			releaseScanScratch(scratch)
			return nil, NewQueryExceptionWithCause(scanErr, fmt.Sprintf("标量查询扫描失败: row=%d", rowIndex))
		}
		raw := *scratch.discardPtr(0)
		releaseScanScratch(scratch)
		converted, convertErr := db.convertToPrimitiveType(raw, targetType)
		if convertErr != nil {
			return nil, NewQueryExceptionWithCause(convertErr, fmt.Sprintf("标量查询转换失败: row=%d", rowIndex))
		}
		typed, ok := converted.(T)
		if !ok {
			return nil, NewQueryException(fmt.Sprintf("标量查询类型不匹配: row=%d, target=%s", rowIndex, targetType))
		}
		results = append(results, typed)
		if !allRows {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "标量查询行遍历失败")
	}
	return results, nil
}

func firstOrZero[T any](values []T) T {
	if len(values) == 0 {
		var zero T
		return zero
	}
	return values[0]
}

func (db *Db) QueryToIntStrictContext(ctx context.Context, query string, params ...any) (int, error) {
	values, err := queryFirstColumnStrictContext[int](db, ctx, query, false, params...)
	return firstOrZero(values), err
}

func (db *Db) QueryToInt64StrictContext(ctx context.Context, query string, params ...any) (int64, error) {
	values, err := queryFirstColumnStrictContext[int64](db, ctx, query, false, params...)
	return firstOrZero(values), err
}

func (db *Db) QueryToFloat64StrictContext(ctx context.Context, query string, params ...any) (float64, error) {
	values, err := queryFirstColumnStrictContext[float64](db, ctx, query, false, params...)
	return firstOrZero(values), err
}

func (db *Db) QueryToStringStrictContext(ctx context.Context, query string, params ...any) (string, error) {
	values, err := queryFirstColumnStrictContext[string](db, ctx, query, false, params...)
	return firstOrZero(values), err
}

func (db *Db) QueryToBoolStrictContext(ctx context.Context, query string, params ...any) (bool, error) {
	values, err := queryFirstColumnStrictContext[bool](db, ctx, query, false, params...)
	return firstOrZero(values), err
}

func (db *Db) QueryToIntSliceStrictContext(ctx context.Context, query string, params ...any) ([]int, error) {
	return queryFirstColumnStrictContext[int](db, ctx, query, true, params...)
}

func (db *Db) QueryToInt64SliceStrictContext(ctx context.Context, query string, params ...any) ([]int64, error) {
	return queryFirstColumnStrictContext[int64](db, ctx, query, true, params...)
}

func (db *Db) QueryToFloat64SliceStrictContext(ctx context.Context, query string, params ...any) ([]float64, error) {
	return queryFirstColumnStrictContext[float64](db, ctx, query, true, params...)
}

func (db *Db) QueryToStringSliceStrictContext(ctx context.Context, query string, params ...any) ([]string, error) {
	return queryFirstColumnStrictContext[string](db, ctx, query, true, params...)
}

func (db *Db) QueryToIntStrict(query string, params ...any) (int, error) {
	ctx, cancel := strictQueryContext()
	defer cancel()
	return db.QueryToIntStrictContext(ctx, query, params...)
}

func (db *Db) QueryToInt64Strict(query string, params ...any) (int64, error) {
	ctx, cancel := strictQueryContext()
	defer cancel()
	return db.QueryToInt64StrictContext(ctx, query, params...)
}

func (db *Db) QueryToFloat64Strict(query string, params ...any) (float64, error) {
	ctx, cancel := strictQueryContext()
	defer cancel()
	return db.QueryToFloat64StrictContext(ctx, query, params...)
}

func (db *Db) QueryToStringStrict(query string, params ...any) (string, error) {
	ctx, cancel := strictQueryContext()
	defer cancel()
	return db.QueryToStringStrictContext(ctx, query, params...)
}

func (db *Db) QueryToBoolStrict(query string, params ...any) (bool, error) {
	ctx, cancel := strictQueryContext()
	defer cancel()
	return db.QueryToBoolStrictContext(ctx, query, params...)
}

func (db *Db) QueryToIntSliceStrict(query string, params ...any) ([]int, error) {
	ctx, cancel := strictQueryContext()
	defer cancel()
	return db.QueryToIntSliceStrictContext(ctx, query, params...)
}

func (db *Db) QueryToInt64SliceStrict(query string, params ...any) ([]int64, error) {
	ctx, cancel := strictQueryContext()
	defer cancel()
	return db.QueryToInt64SliceStrictContext(ctx, query, params...)
}

func (db *Db) QueryToFloat64SliceStrict(query string, params ...any) ([]float64, error) {
	ctx, cancel := strictQueryContext()
	defer cancel()
	return db.QueryToFloat64SliceStrictContext(ctx, query, params...)
}

func (db *Db) QueryToStringSliceStrict(query string, params ...any) ([]string, error) {
	ctx, cancel := strictQueryContext()
	defer cancel()
	return db.QueryToStringSliceStrictContext(ctx, query, params...)
}
