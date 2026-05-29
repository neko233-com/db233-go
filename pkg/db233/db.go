package db233

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"reflect"
	"strconv"
)

// =====================================================
// API 接口定义
// =====================================================

// DbApi 定义数据库操作的统一抽象
// 提供三层操作接口：底层 SQL、ORM、和便利方法
type DbApi interface {
	// 获取底层数据源
	GetDataSource() *sql.DB

	// ========== 最底层 Native SQL 接口 ==========
	// ExecuteSqlByStatement 最底层：执行原生 SQL 并返回原始行数据
	ExecuteSqlByStatement(statement *SqlStatement) []map[string]any

	// ========== ORM 接口 ==========
	// Query ORM 快捷查询
	Query(sql string, params ...any) []map[string]any
	// Save 保存实体
	Save(entity any) error
	// Update 更新实体
	Update(entity any) error
	// Delete 删除实体
	Delete(entity any) error
	// FindById 按 ID 查询
	FindById(id any, entity any) error

	// ========== 对外原生 SQL 接口 ==========
	// ExecuteQuery 执行占位符 SQL 并使用批量参数进行多组查询，返回映射后的结果列表（向后兼容）
	ExecuteQuery(query string, paramsArray [][]any, returnType any) []any
	// ExecuteQueryByStatement 使用 SqlStatement 执行查询并返回 ORM 映射结果
	ExecuteQueryByStatement(statement *SqlStatement) []map[string]any
	// ExecuteUpdateByStatement 使用 SqlStatement 执行更新语句，返回影响行数
	ExecuteUpdateByStatement(statement *SqlStatement) int
	// ExecuteUpdateMultiRows 使用 SQL 与多行参数执行批量更新，返回总影响行数
	ExecuteUpdateMultiRows(query string, multiRowParams [][]any) int
	// ExecuteUpdateMultiRowsNamed 使用 SQL 与多行命名参数执行批量更新，返回总影响行数
	ExecuteUpdateMultiRowsNamed(sql string, paramsList []map[string]any) int
	// ExecuteUpdateNamed 使用 SQL 与命名参数执行更新语句，返回影响行数
	ExecuteUpdateNamed(sql string, params map[string]any) (int64, error)
	// ExecuteWithConnection 提供对底层 *sql.Conn 的回调
	ExecuteWithConnection(fn func(*sql.Conn) error) error
}

// Db 是数据库操作核心类型，封装了数据源、数据库分组、容错管理器等信息。
// Db 对象负责执行 SQL、管理容错逻辑与辅助方法。
type Db struct {
	DataSource   *sql.DB
	DbId         int
	DbGroup      *DbGroup
	DatabaseType EnumDatabaseType // 数据库类型，默认为 MySQL
	// FaultTolerantMgr 容错管理器（可选）
	FaultTolerantMgr *FaultTolerantManager
	// WriteJournal 本地 WAL（可选，游戏服数据不丢）
	WriteJournal *LocalWriteJournal
}

// NewDb 创建一个默认使用 MySQL 的 Db 实例。
func NewDb(dataSource *sql.DB, dbId int, dbGroup *DbGroup) *Db {
	return &Db{
		DataSource:   dataSource,
		DbId:         dbId,
		DbGroup:      dbGroup,
		DatabaseType: EnumDatabaseTypeMySQL, // 默认 MySQL
	}
}

// NewDbWithType 创建一个带指定数据库类型的 Db 实例。
func NewDbWithType(dataSource *sql.DB, dbId int, dbGroup *DbGroup, dbType EnumDatabaseType) *Db {
	if dbType == "" || !dbType.IsValid() {
		dbType = EnumDatabaseTypeMySQL
	}
	return &Db{
		DataSource:   dataSource,
		DbId:         dbId,
		DbGroup:      dbGroup,
		DatabaseType: dbType,
	}
}

// GetDataSource 返回底层的 *sql.DB 数据源。
func (db *Db) GetDataSource() *sql.DB {
	return db.DataSource
}

// =====================================================
// 第一层：底层 Native SQL 执行
// =====================================================

// ExecuteSqlByStatement 最底层：执行 SqlStatement 中的 SQL 并返回原始行数据（map 格式）
// 这是所有其他查询方法的基础
func (db *Db) ExecuteSqlByStatement(statement *SqlStatement) []map[string]any {
	if !statement.IsQuery {
		return nil
	}

	var results []map[string]any

	// 执行查询语句（不使用 ORM 映射）
	for _, sqlStr := range statement.SqlList {
		rows, err := db.DataSource.Query(sqlStr)
		if err != nil {
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, sqlStr)
				if db.FaultTolerantMgr != nil {
					db.FaultTolerantMgr.CheckAndReconnect()
				}
			} else {
				LogError("查询执行失败: %v (SQL: %s)", err, sqlStr)
			}
			continue
		}

		func() {
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil {
				LogError("获取列名失败: %v", err)
				return
			}

			for rows.Next() {
				// 创建扫描目标
				scanTargets := make([]any, len(columns))
				for i := range scanTargets {
					scanTargets[i] = new(any)
				}

				if err := rows.Scan(scanTargets...); err != nil {
					LogError("扫描行失败: %v", err)
					continue
				}

				// 构建 map[string]any 结果
				rowMap := make(map[string]any)
				for i, col := range columns {
					rowMap[col] = *scanTargets[i].(*any)
				}
				results = append(results, rowMap)
			}
		}()
	}

	return results
}

// ExecuteUpdate 底层：执行更新/插入/删除操作，返回影响行数
func (db *Db) ExecuteUpdate(query string, params ...any) (int64, error) {
	defer func() {
		if r := recover(); r != nil {
			LogError("更新执行发生 panic: %v, SQL=%s", r, query)
		}
	}()

	result, err := db.DataSource.Exec(query, params...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, query)
			if db.FaultTolerantMgr != nil {
				db.FaultTolerantMgr.RecordFailedOperation(&FailedOperation{
					Operation: "ExecuteUpdate",
					SQL:       query,
					Params:    params,
					TableName: "",
				})
				db.FaultTolerantMgr.CheckAndReconnect()
			}
			return 0, NewQueryExceptionWithCause(err, "数据库连接已关闭或不可用，请检查网络连接")
		}
		LogError("更新执行失败: %v (SQL: %s)", err, query)
		return 0, NewQueryExceptionWithCause(err, fmt.Sprintf("执行更新失败: %s", query))
	}

	affected, err := result.RowsAffected()
	if err != nil {
		LogError("获取影响行数失败: %v", err)
		return 0, err
	}

	return affected, nil
}

// =====================================================
// 第二层：ORM 快捷方法
// =====================================================

// Query ORM 快捷查询：执行 SQL 并返回原始行数据
func (db *Db) Query(sql string, params ...any) []map[string]any {
	stmt := NewQueryStatement(sql, nil)
	return db.ExecuteSqlByStatement(stmt)
}

// =====================================================
// 第三层：便利方法 - 标量查询（直接返回基本类型）
// =====================================================

// QueryToInt 查询返回单个 int 值
func (db *Db) QueryToInt(sql string, params ...any) int {
	return db.executeQueryToScalar(sql, params, int(0)).(int)
}

// QueryToInt64 查询返回单个 int64 值
func (db *Db) QueryToInt64(sql string, params ...any) int64 {
	return db.executeQueryToScalar(sql, params, int64(0)).(int64)
}

// QueryToFloat64 查询返回单个 float64 值
func (db *Db) QueryToFloat64(sql string, params ...any) float64 {
	return db.executeQueryToScalar(sql, params, float64(0)).(float64)
}

// QueryToString 查询返回单个 string 值
func (db *Db) QueryToString(sql string, params ...any) string {
	return db.executeQueryToScalar(sql, params, "").(string)
}

// QueryToBool 查询返回单个 bool 值
func (db *Db) QueryToBool(sql string, params ...any) bool {
	return db.executeQueryToScalar(sql, params, false).(bool)
}

// QueryToIntSlice 查询返回多个 int 值
func (db *Db) QueryToIntSlice(sql string, params ...any) []int {
	rows, err := db.DataSource.Query(sql, params...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, sql)
			if db.FaultTolerantMgr != nil {
				db.FaultTolerantMgr.CheckAndReconnect()
			}
		} else {
			LogError("查询执行失败: %v (SQL: %s)", err, sql)
		}
		return []int{}
	}
	defer rows.Close()

	var results []int
	columns, err := rows.Columns()
	if err != nil {
		LogError("获取列名失败: %v", err)
		return results
	}

	for rows.Next() {
		scanTargets := make([]any, len(columns))
		for i := range scanTargets {
			scanTargets[i] = new(any)
		}

		if err := rows.Scan(scanTargets...); err != nil {
			LogError("扫描行失败: %v", err)
			continue
		}

		rawValue := *scanTargets[0].(*any)
		convertedValue, err := db.convertToPrimitiveType(rawValue, reflect.TypeOf(int(0)))
		if err != nil {
			LogError("转换失败: %v", err)
			continue
		}
		results = append(results, convertedValue.(int))
	}

	return results
}

// QueryToInt64Slice 查询返回多个 int64 值
func (db *Db) QueryToInt64Slice(sql string, params ...any) []int64 {
	rows, err := db.DataSource.Query(sql, params...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, sql)
			if db.FaultTolerantMgr != nil {
				db.FaultTolerantMgr.CheckAndReconnect()
			}
		} else {
			LogError("查询执行失败: %v (SQL: %s)", err, sql)
		}
		return []int64{}
	}
	defer rows.Close()

	var results []int64
	columns, err := rows.Columns()
	if err != nil {
		LogError("获取列名失败: %v", err)
		return results
	}

	for rows.Next() {
		scanTargets := make([]any, len(columns))
		for i := range scanTargets {
			scanTargets[i] = new(any)
		}

		if err := rows.Scan(scanTargets...); err != nil {
			LogError("扫描行失败: %v", err)
			continue
		}

		rawValue := *scanTargets[0].(*any)
		convertedValue, err := db.convertToPrimitiveType(rawValue, reflect.TypeOf(int64(0)))
		if err != nil {
			LogError("转换失败: %v", err)
			continue
		}
		results = append(results, convertedValue.(int64))
	}

	return results
}

// QueryToFloat64Slice 查询返回多个 float64 值
func (db *Db) QueryToFloat64Slice(sql string, params ...any) []float64 {
	rows, err := db.DataSource.Query(sql, params...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, sql)
			if db.FaultTolerantMgr != nil {
				db.FaultTolerantMgr.CheckAndReconnect()
			}
		} else {
			LogError("查询执行失败: %v (SQL: %s)", err, sql)
		}
		return []float64{}
	}
	defer rows.Close()

	var results []float64
	columns, err := rows.Columns()
	if err != nil {
		LogError("获取列名失败: %v", err)
		return results
	}

	for rows.Next() {
		scanTargets := make([]any, len(columns))
		for i := range scanTargets {
			scanTargets[i] = new(any)
		}

		if err := rows.Scan(scanTargets...); err != nil {
			LogError("扫描行失败: %v", err)
			continue
		}

		rawValue := *scanTargets[0].(*any)
		convertedValue, err := db.convertToPrimitiveType(rawValue, reflect.TypeOf(float64(0)))
		if err != nil {
			LogError("转换失败: %v", err)
			continue
		}
		results = append(results, convertedValue.(float64))
	}

	return results
}

// QueryToStringSlice 查询返回多个 string 值
func (db *Db) QueryToStringSlice(sql string, params ...any) []string {
	rows, err := db.DataSource.Query(sql, params...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, sql)
			if db.FaultTolerantMgr != nil {
				db.FaultTolerantMgr.CheckAndReconnect()
			}
		} else {
			LogError("查询执行失败: %v (SQL: %s)", err, sql)
		}
		return []string{}
	}
	defer rows.Close()

	var results []string
	columns, err := rows.Columns()
	if err != nil {
		LogError("获取列名失败: %v", err)
		return results
	}

	for rows.Next() {
		scanTargets := make([]any, len(columns))
		for i := range scanTargets {
			scanTargets[i] = new(any)
		}

		if err := rows.Scan(scanTargets...); err != nil {
			LogError("扫描行失败: %v", err)
			continue
		}

		rawValue := *scanTargets[0].(*any)
		convertedValue, err := db.convertToPrimitiveType(rawValue, reflect.TypeOf(""))
		if err != nil {
			LogError("转换失败: %v", err)
			continue
		}
		results = append(results, convertedValue.(string))
	}

	return results
}

// ExecuteQueryContext 使用指定的 context 执行查询，支持批量参数集。
// 如果 paramsArray 为空，将执行一次无参数查询。
// 如果 returnType 为 nil，将返回原始值（用于 COUNT、SUM 等聚合查询）。
func (db *Db) ExecuteQueryContext(ctx context.Context, query string, paramsArray [][]any, returnType any) []any {
	defer func() {
		if r := recover(); r != nil {
			LogError("查询执行发生 panic: %v, SQL=%s", r, query)
		}
	}()
	var results []any

	// 如果没有提供参数数组，或者提供了空的参数数组，仍然需要执行一次 SQL（无参数）
	if len(paramsArray) == 0 {
		paramsArray = [][]any{{}}
	}

	// 检测 returnType 是否为基础类型（用于 OLAP 查询如 COUNT、SUM 等）
	if db.isPrimitiveType(returnType) {
		return db.executeQueryPrimitive(ctx, query, paramsArray, returnType)
	}

	// 如果 returnType 为 nil，执行原始值查询（返回原始值或 map）
	if returnType == nil {
		return db.executeQueryRaw(ctx, query, paramsArray)
	}

	for _, params := range paramsArray {
		rows, err := db.DataSource.QueryContext(ctx, query, params...)
		if err != nil {
			// 友好的错误提示
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, query)
				if db.FaultTolerantMgr != nil {
					db.FaultTolerantMgr.CheckAndReconnect()
				}
			} else {
				LogError("查询执行失败: %v (SQL: %s)", err, query)
			}
			continue
		}

		// 确保 rows 在本次迭代结束时被关闭，避免延迟到函数退出
		func() {
			defer rows.Close()
			// 使用 ORM 映射（假设 OrmBatch 会消费 rows）
			batchResults := OrmHandlerInstance.OrmBatch(rows, returnType)
			results = append(results, batchResults...)
		}()
	}
	return results
}

// isPrimitiveType 检测是否为基础类型（int, int64, float64, string, bool 等）
func (db *Db) isPrimitiveType(returnType any) bool {
	if returnType == nil {
		return false
	}

	t := reflect.TypeOf(returnType)
	// 处理指针类型
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	kind := t.Kind()
	// 检查是否为基础类型
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64,
		reflect.String,
		reflect.Bool:
		return true
	default:
		return false
	}
}

// executeQueryPrimitive 执行基础类型查询（用于 COUNT、SUM 等 OLAP 查询）
// 只返回第一个值，并转换为指定的基础类型
func (db *Db) executeQueryPrimitive(ctx context.Context, query string, paramsArray [][]any, returnType any) []any {
	var results []any
	targetType := reflect.TypeOf(returnType)
	if targetType.Kind() == reflect.Ptr {
		targetType = targetType.Elem()
	}

	for _, params := range paramsArray {
		rows, err := db.DataSource.QueryContext(ctx, query, params...)
		if err != nil {
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, query)
				if db.FaultTolerantMgr != nil {
					db.FaultTolerantMgr.CheckAndReconnect()
				}
			} else {
				LogError("基础类型查询执行失败: %v (SQL: %s)", err, query)
			}
			continue
		}

		func() {
			defer rows.Close()
			// 只处理第一行，忽略别名，直接取第一个值
			if rows.Next() {
				// 获取列数
				columns, err := rows.Columns()
				if err != nil {
					LogError("获取列名失败: %v", err)
					return
				}

				// 创建扫描目标（扫描所有列，但只使用第一列）
				scanTargets := make([]any, len(columns))
				for i := range scanTargets {
					scanTargets[i] = new(any)
				}

				err = rows.Scan(scanTargets...)
				if err != nil {
					LogError("扫描基础类型值失败: %v", err)
					return
				}

				// 只取第一列的值
				rawValue := *scanTargets[0].(*any)

				// 转换为目标类型
				convertedValue, err := db.convertToPrimitiveType(rawValue, targetType)
				if err != nil {
					LogError("转换基础类型失败: %v, 目标类型=%s", err, targetType)
					return
				}
				results = append(results, convertedValue)
			}
		}()
	}
	return results
}

// convertToPrimitiveType 将原始值转换为指定的基础类型
func (db *Db) convertToPrimitiveType(rawValue any, targetType reflect.Type) (any, error) {
	if rawValue == nil {
		// 返回目标类型的零值
		return reflect.Zero(targetType).Interface(), nil
	}

	rawVal := reflect.ValueOf(rawValue)

	// 处理 []uint8 (MySQL 返回的字节数组)
	if rawVal.Kind() == reflect.Slice && rawVal.Type().Elem().Kind() == reflect.Uint8 {
		str := string(rawValue.([]byte))
		return db.convertStringToPrimitive(str, targetType)
	}

	// 如果类型匹配，直接返回
	if rawVal.Type().AssignableTo(targetType) {
		return rawValue, nil
	}

	// 尝试转换
	if rawVal.Type().ConvertibleTo(targetType) {
		return rawVal.Convert(targetType).Interface(), nil
	}

	// 如果是字符串，尝试解析
	if rawVal.Kind() == reflect.String {
		return db.convertStringToPrimitive(rawVal.String(), targetType)
	}

	return nil, fmt.Errorf("无法将 %T 转换为 %s", rawValue, targetType)
}

// convertStringToPrimitive 将字符串转换为指定的基础类型
func (db *Db) convertStringToPrimitive(str string, targetType reflect.Type) (any, error) {
	targetKind := targetType.Kind()

	switch targetKind {
	case reflect.String:
		return str, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		val, err := strconv.ParseInt(str, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("无法将字符串转换为整数: %w", err)
		}
		return reflect.ValueOf(val).Convert(targetType).Interface(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		val, err := strconv.ParseUint(str, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("无法将字符串转换为无符号整数: %w", err)
		}
		return reflect.ValueOf(val).Convert(targetType).Interface(), nil
	case reflect.Float32, reflect.Float64:
		val, err := strconv.ParseFloat(str, 64)
		if err != nil {
			return nil, fmt.Errorf("无法将字符串转换为浮点数: %w", err)
		}
		return reflect.ValueOf(val).Convert(targetType).Interface(), nil
	case reflect.Bool:
		val, err := strconv.ParseBool(str)
		if err != nil {
			return nil, fmt.Errorf("无法将字符串转换为布尔值: %w", err)
		}
		return val, nil
	default:
		return nil, fmt.Errorf("不支持的目标类型: %s", targetType)
	}
}

// executeQueryRaw 执行原始值查询（用于 COUNT、SUM 等聚合查询）
func (db *Db) executeQueryRaw(ctx context.Context, query string, paramsArray [][]any) []any {
	var results []any
	for _, params := range paramsArray {
		rows, err := db.DataSource.QueryContext(ctx, query, params...)
		if err != nil {
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, query)
				if db.FaultTolerantMgr != nil {
					db.FaultTolerantMgr.CheckAndReconnect()
				}
			} else {
				LogError("原始查询执行失败: %v (SQL: %s)", err, query)
			}
			continue
		}

		func() {
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil {
				LogError("获取列名失败: %v", err)
				return
			}

			for rows.Next() {
				// 创建扫描目标
				scanTargets := make([]any, len(columns))
				for i := range scanTargets {
					scanTargets[i] = new(any)
				}

				if err := rows.Scan(scanTargets...); err != nil {
					LogError("扫描行失败: %v", err)
					continue
				}

				// 如果只有一列，直接返回该值；否则返回 map
				if len(columns) == 1 {
					val := *scanTargets[0].(*any)
					results = append(results, val)
				} else {
					rowMap := make(map[string]any)
					for i, col := range columns {
						rowMap[col] = *scanTargets[i].(*any)
					}
					results = append(results, rowMap)
				}
			}
		}()
	}
	return results
}

// ExecuteQueryVariadic 使用单组可变参数执行查询并返回映射结果。
func (db *Db) ExecuteQueryVariadic(query string, returnType any, params ...any) []any {
	// 将可变参数包装成单条 paramsArray
	return db.ExecuteQueryContext(context.Background(), query, [][]any{params}, returnType)
}

// ExecuteQueryTyped 执行查询并返回泛型类型切片，适用于 Go 泛型调用。
// 使用示例：ExecuteQueryTyped[MyEntity](db, ctx, "SELECT ...", params...)
func ExecuteQueryTyped[T any](db *Db, ctx context.Context, query string, params ...any) ([]T, error) {
	var tPtr *T
	results := db.ExecuteQueryContext(ctx, query, [][]any{params}, tPtr)
	out := make([]T, 0, len(results))
	for i, r := range results {
		switch v := r.(type) {
		case T:
			out = append(out, v)
		case *T:
			if v == nil {
				continue
			}
			out = append(out, *v)
		default:
			return nil, fmt.Errorf("结果无法转换为目标类型 (index=%d): %T", i, r)
		}
	}
	return out, nil
}

// ExecuteQueryByStatement 使用 SqlStatement 执行查询并返回映射结果。
// 返回 []map[string]any 格式的原始查询结果，不进行 ORM 映射。
func (db *Db) ExecuteQueryByStatement(statement *SqlStatement) []map[string]any {
	if !statement.IsQuery {
		return nil
	}

	var results []map[string]any

	// 执行查询语句（不使用 ORM 映射）
	for _, sqlStr := range statement.SqlList {
		rows, err := db.DataSource.Query(sqlStr)
		if err != nil {
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, sqlStr)
				if db.FaultTolerantMgr != nil {
					db.FaultTolerantMgr.CheckAndReconnect()
				}
			} else {
				LogError("查询执行失败: %v (SQL: %s)", err, sqlStr)
			}
			continue
		}

		func() {
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil {
				LogError("获取列名失败: %v", err)
				return
			}

			for rows.Next() {
				// 创建扫描目标
				scanTargets := make([]any, len(columns))
				for i := range scanTargets {
					scanTargets[i] = new(any)
				}

				if err := rows.Scan(scanTargets...); err != nil {
					LogError("扫描行失败: %v", err)
					continue
				}

				// 构建 map[string]any 结果
				rowMap := make(map[string]any)
				for i, col := range columns {
					rowMap[col] = *scanTargets[i].(*any)
				}
				results = append(results, rowMap)
			}
		}()
	}

	return results
}

// ExecuteUpdateByStatement 使用 SqlStatement 执行更新语句，返回受影响行数。
func (db *Db) ExecuteUpdateByStatement(statement *SqlStatement) int {
	if statement.IsQuery {
		return 0
	}
	totalAffected := 0
	for _, q := range statement.SqlList {
		result, err := db.DataSource.Exec(q)
		if err != nil {
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, q)
				if db.FaultTolerantMgr != nil {
					db.FaultTolerantMgr.RecordFailedOperation(&FailedOperation{
						Operation: "ExecuteUpdate",
						SQL:       q,
						Params:    []any{},
						TableName: "",
					})
					db.FaultTolerantMgr.CheckAndReconnect()
				}
			} else {
				log.Printf("ExecuteUpdate error: %v", err)
			}
			continue
		}
		affected, _ := result.RowsAffected()
		totalAffected += int(affected)
	}
	return totalAffected
}

// ExecuteUpdateMultiRows 使用 SQL 与多行参数执行批量更新，返回总影响行数。
func (db *Db) ExecuteUpdateMultiRows(query string, multiRowParams [][]any) int {
	defer func() {
		if r := recover(); r != nil {
			LogError("批量更新发生 panic: %v, SQL=%s", r, query)
		}
	}()
	totalAffected := 0
	for _, params := range multiRowParams {
		result, err := db.DataSource.Exec(query, params...)
		if err != nil {
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, query)
				if db.FaultTolerantMgr != nil {
					db.FaultTolerantMgr.RecordFailedOperation(&FailedOperation{
						Operation: "ExecuteUpdate",
						SQL:       query,
						Params:    toAnySlice(params),
						TableName: "",
					})
					db.FaultTolerantMgr.CheckAndReconnect()
				}
			} else {
				log.Printf("Db233 ExecuteUpdateMultiRows error: %v", err)
			}
			continue
		}
		affected, _ := result.RowsAffected()
		totalAffected += int(affected)
	}
	return totalAffected
}

// ExecuteUpdateMultiRowsNamed 使用 SQL 与多行命名参数执行批量更新，返回总影响行数。
// 使用命名参数方式，SQL 中用 {paramName} 表示占位符，参数通过 []map[string]any 传递
// 例如：sql = "UPDATE users SET name={name}, age={age} WHERE id={userId}"
//
//	params = []map[string]any{
//	    {"name": "Alice", "age": 25, "userId": 1},
//	    {"name": "Bob", "age": 30, "userId": 2},
//	}
func (db *Db) ExecuteUpdateMultiRowsNamed(sql string, paramsList []map[string]any) int {
	defer func() {
		if r := recover(); r != nil {
			LogError("批量更新发生 panic: %v, SQL=%s", r, sql)
		}
	}()
	totalAffected := 0
	for _, params := range paramsList {
		newSQL, values, err := replaceSqlNamedParameters(sql, params)
		if err != nil {
			LogError("参数替换失败: %v", err)
			continue
		}

		result, err := db.DataSource.Exec(newSQL, values...)
		if err != nil {
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, sql)
				if db.FaultTolerantMgr != nil {
					db.FaultTolerantMgr.RecordFailedOperation(&FailedOperation{
						Operation: "ExecuteUpdate",
						SQL:       sql,
						Params:    values,
						TableName: "",
					})
					db.FaultTolerantMgr.CheckAndReconnect()
				}
			} else {
				log.Printf("Db233 ExecuteUpdateMultiRowsNamed error: %v", err)
			}
			continue
		}
		affected, _ := result.RowsAffected()
		totalAffected += int(affected)
	}
	return totalAffected
}

// ExecuteOriginalUpdate 向后兼容：ExecuteUpdateMultiRows 的别名
func (db *Db) ExecuteOriginalUpdate(query string, multiRowParams [][]any) int {
	return db.ExecuteUpdateMultiRows(query, multiRowParams)
}

// ExecuteWithConnection 提供对低级 *sql.Conn 的回调入口。
func (db *Db) ExecuteWithConnection(fn func(*sql.Conn) error) error {
	conn, err := db.DataSource.Conn(context.TODO())
	if err != nil {
		return err
	}
	defer conn.Close()
	return fn(conn)
}

// ExecuteQuery 使用批量参数集合执行查询（每组参数单独执行一次），并将结果映射为 returnType 指定的类型。
func (db *Db) ExecuteQuery(query string, paramsArray [][]any, returnType any) []any {
	// 保持向后兼容：默认使用 background context
	return db.ExecuteQueryContext(context.Background(), query, paramsArray, returnType)
}

// ExecuteQuerySingle 执行单行查询并返回结果，找不到时返回类型默认值。
func (db *Db) ExecuteQuerySingle(query string, params []any, returnType any) any {
	results := db.ExecuteQuery(query, [][]any{params}, returnType)
	if len(results) > 0 {
		return results[0]
	}
	return getDefaultValue(returnType)
}

// ExecuteQuerySingleOrNull 执行单行查询并返回结果或 nil。
func (db *Db) ExecuteQuerySingleOrNull(query string, params []any, returnType any) any {
	results := db.ExecuteQuery(query, [][]any{params}, returnType)
	if len(results) > 0 {
		return results[0]
	}
	return nil
}

// ExecuteQuery 简化版查询方法（单组参数）
func (db *Db) ExecuteQuerySimple(query string, params []any, returnType any) []any {
	return db.ExecuteQuery(query, [][]any{params}, returnType)
}

// Close 关闭底层数据库连接，并在需要时停止容错管理器与 WAL。
func (db *Db) Close() error {
	if db.WriteJournal != nil {
		db.WriteJournal.Stop()
	}
	if db.FaultTolerantMgr != nil {
		db.FaultTolerantMgr.Stop()
	}
	return db.DataSource.Close()
}

// EnableFaultTolerance 启用容错管理器。
func (db *Db) EnableFaultTolerance(dbConfig *DbConnectionConfig) {
	if db == nil || dbConfig == nil {
		LogWarn("容错管理器启用失败: db 或 dbConfig 为空")
		return
	}
	if db.FaultTolerantMgr != nil {
		return
	}
	db.FaultTolerantMgr = NewFaultTolerantManager(db, dbConfig)
	db.FaultTolerantMgr.Start()
	LogInfo("容错管理器已启用")
}

// DisableFaultTolerance 停用容错管理器。
func (db *Db) DisableFaultTolerance() {
	if db.FaultTolerantMgr == nil {
		return
	}
	db.FaultTolerantMgr.Stop()
	db.FaultTolerantMgr = nil
	LogInfo("容错管理器已停用")
}

// toAnySlice 辅助函数，将 []any 复制为新的 []any 切片。
func toAnySlice(params []any) []any {
	if len(params) == 0 {
		return []any{}
	}
	result := make([]any, 0, len(params))
	for _, v := range params {
		result = append(result, v)
	}
	return result
}

// getDefaultValue 返回常见类型的默认 Go 值（用于单行查询未命中时）。
func getDefaultValue(t any) any {
	switch t.(type) {
	case int:
		return 0
	case int64:
		return int64(0)
	case string:
		return ""
	case bool:
		return false
	default:
		return nil
	}
}

// =====================================================
// 便利查询方法：直接返回标量类型
// =====================================================

// =====================================================
// 向后兼容：旧名称的别名
// =====================================================

// ExecuteQueryToInt 向后兼容：使用 QueryToInt 替代
func (db *Db) ExecuteQueryToInt(sql string, params ...any) int {
	return db.QueryToInt(sql, params...)
}

// ExecuteQueryToInt64 向后兼容：使用 QueryToInt64 替代
func (db *Db) ExecuteQueryToInt64(sql string, params ...any) int64 {
	return db.QueryToInt64(sql, params...)
}

// ExecuteQueryToFloat64 向后兼容：使用 QueryToFloat64 替代
func (db *Db) ExecuteQueryToFloat64(sql string, params ...any) float64 {
	return db.QueryToFloat64(sql, params...)
}

// ExecuteQueryToString 向后兼容：使用 QueryToString 替代
func (db *Db) ExecuteQueryToString(sql string, params ...any) string {
	return db.QueryToString(sql, params...)
}

// ExecuteQueryToBool 向后兼容：使用 QueryToBool 替代
func (db *Db) ExecuteQueryToBool(sql string, params ...any) bool {
	return db.QueryToBool(sql, params...)
}

// ExecuteQueryToIntSlice 向后兼容：使用 QueryToIntSlice 替代
func (db *Db) ExecuteQueryToIntSlice(sql string, params ...any) []int {
	return db.QueryToIntSlice(sql, params...)
}

// ExecuteQueryToInt64Slice 向后兼容：使用 QueryToInt64Slice 替代
func (db *Db) ExecuteQueryToInt64Slice(sql string, params ...any) []int64 {
	return db.QueryToInt64Slice(sql, params...)
}

// ExecuteQueryToFloat64Slice 向后兼容：使用 QueryToFloat64Slice 替代
func (db *Db) ExecuteQueryToFloat64Slice(sql string, params ...any) []float64 {
	return db.QueryToFloat64Slice(sql, params...)
}

// ExecuteQueryToStringSlice 向后兼容：使用 QueryToStringSlice 替代
func (db *Db) ExecuteQueryToStringSlice(sql string, params ...any) []string {
	return db.QueryToStringSlice(sql, params...)
}

// ExecuteQueryToInt64ByStatement 向后兼容：使用 ExecuteSqlByStatement 替代
func (db *Db) ExecuteQueryToInt64ByStatement(statement *SqlStatement) int64 {
	rows := db.ExecuteSqlByStatement(statement)
	if len(rows) > 0 {
		return db.extractScalarValue(rows[0], int64(0)).(int64)
	}
	return 0
}

// ExecuteQueryToStringByStatement 向后兼容：使用 ExecuteSqlByStatement 替代
func (db *Db) ExecuteQueryToStringByStatement(statement *SqlStatement) string {
	rows := db.ExecuteSqlByStatement(statement)
	if len(rows) > 0 {
		return db.extractScalarValue(rows[0], "").(string)
	}
	return ""
}

// =====================================================
// 内部辅助方法：类型转换工具
// =====================================================

// executeQueryToScalar 通用的标量类型查询方法
// 执行查询并从第一行第一列获取值，然后转换为指定的基础类型
// defaultValue: 用来推断目标类型，查询无结果时返回该类型的零值
func (db *Db) executeQueryToScalar(sql string, params []any, defaultValue any) any {
	rows, err := db.DataSource.Query(sql, params...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, sql)
			if db.FaultTolerantMgr != nil {
				db.FaultTolerantMgr.CheckAndReconnect()
			}
		} else {
			LogError("标量查询执行失败: %v (SQL: %s)", err, sql)
		}
		return defaultValue
	}
	defer rows.Close()

	// 获取列信息
	columns, err := rows.Columns()
	if err != nil {
		LogError("获取列名失败: %v", err)
		return defaultValue
	}

	// 如果没有行，返回默认值
	if !rows.Next() {
		return defaultValue
	}

	// 创建扫描目标（只需要第一列）
	scanTargets := make([]any, len(columns))
	for i := range scanTargets {
		scanTargets[i] = new(any)
	}

	if err := rows.Scan(scanTargets...); err != nil {
		LogError("扫描标量值失败: %v", err)
		return defaultValue
	}

	// 获取第一列的原始值
	rawValue := *scanTargets[0].(*any)

	// 推断目标类型并转换
	targetType := reflect.TypeOf(defaultValue)
	convertedValue, err := db.convertToPrimitiveType(rawValue, targetType)
	if err != nil {
		LogError("转换标量类型失败: %v, 目标类型=%s", err, targetType)
		return defaultValue
	}

	return convertedValue
}

// replaceSqlNamedParameters 将 SQL 中的命名占位符 {paramName} 替换为 ? 并返回参数值数组
// 例如：SQL="SELECT * FROM users WHERE id={userId} AND name={userName}"
// params=map{"userId": 123, "userName": "Alice"}
// 返回：newSQL="SELECT * FROM users WHERE id=? AND name=?", values=[123, "Alice"]
func replaceSqlNamedParameters(sql string, params map[string]any) (string, []any, error) {
	var newSQL string
	var values []any
	i := 0

	for i < len(sql) {
		// 查找下一个占位符
		startIdx := -1
		for j := i; j < len(sql); j++ {
			if sql[j] == '{' {
				startIdx = j
				break
			}
		}

		if startIdx == -1 {
			// 没有更多占位符，直接添加剩余部分
			newSQL += sql[i:]
			break
		}

		// 找到结束的 }
		endIdx := -1
		for j := startIdx + 1; j < len(sql); j++ {
			if sql[j] == '}' {
				endIdx = j
				break
			}
		}

		if endIdx == -1 {
			return "", nil, fmt.Errorf("SQL 中存在未闭合的占位符：%s", sql[startIdx:])
		}

		// 提取参数名
		paramName := sql[startIdx+1 : endIdx]

		// 检查参数是否存在
		value, exists := params[paramName]
		if !exists {
			return "", nil, fmt.Errorf("缺少必需的参数：%s", paramName)
		}

		// 添加 SQL 片段和替换占位符
		newSQL += sql[i:startIdx] + "?"
		values = append(values, value)

		i = endIdx + 1
	}

	return newSQL, values, nil
}

// =====================================================
// 命名参数查询方法
// =====================================================

// QueryNamed 执行带命名参数的 SQL 查询
// 例如：db.QueryNamed("SELECT * FROM users WHERE id={userId} AND status={status}", map[string]any{"userId": 123, "status": "active"})
func (db *Db) QueryNamed(sql string, params map[string]any) []map[string]any {
	newSQL, values, err := replaceSqlNamedParameters(sql, params)
	if err != nil {
		LogError("参数替换失败: %v", err)
		return []map[string]any{}
	}

	// 直接执行查询
	rows, err := db.DataSource.Query(newSQL, values...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, sql)
			if db.FaultTolerantMgr != nil {
				db.FaultTolerantMgr.CheckAndReconnect()
			}
		} else {
			LogError("查询执行失败: %v (SQL: %s)", err, sql)
		}
		return []map[string]any{}
	}
	defer rows.Close()

	var results []map[string]any
	columns, err := rows.Columns()
	if err != nil {
		LogError("获取列名失败: %v", err)
		return results
	}

	for rows.Next() {
		scanTargets := make([]any, len(columns))
		for i := range scanTargets {
			scanTargets[i] = new(any)
		}

		if err := rows.Scan(scanTargets...); err != nil {
			LogError("扫描行失败: %v", err)
			continue
		}

		rowMap := make(map[string]any)
		for i, col := range columns {
			rowMap[col] = *scanTargets[i].(*any)
		}
		results = append(results, rowMap)
	}

	return results
}

// QueryNamedToInt64 执行带命名参数的 SQL 查询，返回单个 int64 值
func (db *Db) QueryNamedToInt64(sql string, params map[string]any) int64 {
	rows := db.QueryNamed(sql, params)
	if len(rows) > 0 {
		return db.extractScalarValue(rows[0], int64(0)).(int64)
	}
	return 0
}

// QueryNamedToString 执行带命名参数的 SQL 查询，返回单个 string 值
func (db *Db) QueryNamedToString(sql string, params map[string]any) string {
	rows := db.QueryNamed(sql, params)
	if len(rows) > 0 {
		return db.extractScalarValue(rows[0], "").(string)
	}
	return ""
}

// QueryNamedToInt 执行带命名参数的 SQL 查询，返回单个 int 值
func (db *Db) QueryNamedToInt(sql string, params map[string]any) int {
	rows := db.QueryNamed(sql, params)
	if len(rows) > 0 {
		return db.extractScalarValue(rows[0], int(0)).(int)
	}
	return 0
}

// QueryNamedToFloat64 执行带命名参数的 SQL 查询，返回单个 float64 值
func (db *Db) QueryNamedToFloat64(sql string, params map[string]any) float64 {
	rows := db.QueryNamed(sql, params)
	if len(rows) > 0 {
		return db.extractScalarValue(rows[0], float64(0)).(float64)
	}
	return 0
}

// QueryNamedToInt64Slice 执行带命名参数的 SQL 查询，返回 []int64
func (db *Db) QueryNamedToInt64Slice(sql string, params map[string]any) []int64 {
	newSQL, values, err := replaceSqlNamedParameters(sql, params)
	if err != nil {
		LogError("参数替换失败: %v", err)
		return []int64{}
	}

	rows, err := db.DataSource.Query(newSQL, values...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, sql)
			if db.FaultTolerantMgr != nil {
				db.FaultTolerantMgr.CheckAndReconnect()
			}
		} else {
			LogError("查询执行失败: %v (SQL: %s)", err, sql)
		}
		return []int64{}
	}
	defer rows.Close()

	var results []int64
	for rows.Next() {
		var val int64
		if err := rows.Scan(&val); err != nil {
			LogError("扫描行失败: %v", err)
			continue
		}
		results = append(results, val)
	}

	return results
}

// QueryNamedToStringSlice 执行带命名参数的 SQL 查询，返回 []string
func (db *Db) QueryNamedToStringSlice(sql string, params map[string]any) []string {
	newSQL, values, err := replaceSqlNamedParameters(sql, params)
	if err != nil {
		LogError("参数替换失败: %v", err)
		return []string{}
	}

	rows, err := db.DataSource.Query(newSQL, values...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, sql)
			if db.FaultTolerantMgr != nil {
				db.FaultTolerantMgr.CheckAndReconnect()
			}
		} else {
			LogError("查询执行失败: %v (SQL: %s)", err, sql)
		}
		return []string{}
	}
	defer rows.Close()

	var results []string
	for rows.Next() {
		var val string
		if err := rows.Scan(&val); err != nil {
			LogError("扫描行失败: %v", err)
			continue
		}
		results = append(results, val)
	}

	return results
}

// ExecuteUpdateNamed 执行带命名参数的 SQL 更新语句
// 例如：db.ExecuteUpdateNamed("UPDATE users SET name={name} WHERE id={userId}", map[string]any{"name": "Bob", "userId": 123})
func (db *Db) ExecuteUpdateNamed(sql string, params map[string]any) (int64, error) {
	newSQL, values, err := replaceSqlNamedParameters(sql, params)
	if err != nil {
		LogError("参数替换失败: %v", err)
		return 0, err
	}

	result, err := db.DataSource.Exec(newSQL, values...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, sql)
			if db.FaultTolerantMgr != nil {
				db.FaultTolerantMgr.RecordFailedOperation(&FailedOperation{
					Operation: "ExecuteUpdate",
					SQL:       sql,
					Params:    values,
					TableName: "",
				})
				db.FaultTolerantMgr.CheckAndReconnect()
			}
			return 0, NewQueryExceptionWithCause(err, "数据库连接已关闭或不可用，请检查网络连接")
		}
		LogError("更新执行失败: %v (SQL: %s)", err, sql)
		return 0, NewQueryExceptionWithCause(err, fmt.Sprintf("执行更新失败: %s", sql))
	}

	affected, err := result.RowsAffected()
	if err != nil {
		LogError("获取影响行数失败: %v", err)
		return 0, err
	}

	return affected, nil
}

// extractScalarValue 从 map[string]any 中提取第一个值并转换为目标类型
// 默认忽略列名，直接取第一列的值
func (db *Db) extractScalarValue(rowData map[string]any, defaultValue any) any {
	if len(rowData) == 0 {
		return defaultValue
	}

	// 取第一个值（map 的遍历顺序是随机的，但通常数据库返回的顺序是稳定的）
	var rawValue any
	for _, v := range rowData {
		rawValue = v
		break
	}

	if rawValue == nil {
		return defaultValue
	}

	// 推断目标类型并转换
	targetType := reflect.TypeOf(defaultValue)
	convertedValue, err := db.convertToPrimitiveType(rawValue, targetType)
	if err != nil {
		LogError("转换标量值失败: %v, 目标类型=%s", err, targetType)
		return defaultValue
	}

	return convertedValue
}
