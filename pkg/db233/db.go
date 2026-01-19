package db233

import (
	"context"
	"database/sql"
	"log"
)

/**
 * DbApi 接口 - Go 版
 *
 * 定义数据库操作的统一抽象
 *
 * @author neko233-com
 * @since 2025-12-28
 */
type DbApi interface {
	/**
	 * 获取数据源
	 *
	 * @return *sql.DB 数据源
	 */
	GetDataSource() *sql.DB

	/**
	 * 使用占位符 SQL + 批量参数，查询结果列表
	 *
	 * @param sql SQL 语句
	 * @param paramsArray 参数数组
	 * @param returnType 返回类型
	 * @return []any 结果列表
	 */
	ExecuteQuery(sql string, paramsArray [][]any, returnType any) []any

	/**
	 * 使用 SqlStatement 执行查询
	 *
	 * @param statement SQL 语句对象
	 * @return []any 结果列表
	 */
	ExecuteQueryByStatement(statement *SqlStatement) []any

	/**
	 * 使用 SqlStatement 执行更新
	 *
	 * @param statement SQL 语句对象
	 * @return int 影响行数
	 */
	ExecuteUpdateByStatement(statement *SqlStatement) int

	/**
	 * 使用占位符 SQL 批量更新
	 *
	 * @param sql SQL 语句
	 * @param multiRowParams 多行参数
	 * @return int 影响行数
	 */
	ExecuteOriginalUpdate(sql string, multiRowParams [][]any) int

	/**
	 * 提供直接使用 Connection 的回调入口
	 *
	 * @param fn 回调函数
	 * @return error 执行错误
	 */
	ExecuteWithConnection(fn func(*sql.Conn) error) error
}

/**
 * Db 数据库操作核心类 - Go 版
 *
 * 对应 Kotlin 版本的 Db 类
 *
 * @author neko233-com
 * @since 2025-12-28
 */
type Db struct {
	DataSource   *sql.DB
	DbId         int
	DbGroup      *DbGroup
	DatabaseType EnumDatabaseType // 数据库类型，默认为 MySQL
	// FaultTolerantMgr 容错管理器（可选）
	FaultTolerantMgr *FaultTolerantManager
}

/**
 * 创建 Db 实例
 *
 * @param dataSource 数据源
 * @param dbId 数据库 ID
 * @param dbGroup 所属数据库组
 * @return *Db 实例
 */
func NewDb(dataSource *sql.DB, dbId int, dbGroup *DbGroup) *Db {
	return &Db{
		DataSource:   dataSource,
		DbId:         dbId,
		DbGroup:      dbGroup,
		DatabaseType: EnumDatabaseTypeMySQL, // 默认 MySQL
	}
}

/**
 * 创建指定数据库类型的 Db 实例
 *
 * @param dataSource 数据源
 * @param dbId 数据库 ID
 * @param dbGroup 所属数据库组
 * @param dbType 数据库类型
 * @return *Db 实例
 */
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

/**
 * 获取数据源
 *
 * @return *sql.DB 数据源
 */
func (db *Db) GetDataSource() *sql.DB {
	return db.DataSource
}

/**
 * 执行查询（批量参数）
 *
 * @param sql SQL 语句
 * @param paramsArray 参数数组
 * @param returnType 返回类型
 * @return []any 结果列表
 */
func (db *Db) ExecuteQuery(sql string, paramsArray [][]any, returnType any) []any {
	defer func() {
		if r := recover(); r != nil {
			LogError("查询执行发生 panic: %v, SQL=%s", r, sql)
		}
	}()
	var results []any

	// 如果没有提供参数数组，或者提供了空的参数数组，仍然需要执行一次 SQL（无参数）
	if len(paramsArray) == 0 {
		paramsArray = [][]any{{}}
	}

	for _, params := range paramsArray {
		rows, err := db.DataSource.Query(sql, params...)
		if err != nil {
			// 友好的错误提示
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, sql)
				if db.FaultTolerantMgr != nil {
					db.FaultTolerantMgr.CheckAndReconnect()
				}
			} else {
				LogError("查询执行失败: %v (SQL: %s)", err, sql)
			}
			continue
		}

		// 使用 ORM 映射
		batchResults := OrmHandlerInstance.OrmBatch(rows, returnType)
		results = append(results, batchResults...)
	}
	return results
}

// ExecuteQueryByStatement 使用 SqlStatement 执行查询
/**
 * 使用 SqlStatement 执行查询
 *
 * @param statement SQL 语句对象
 * @return []any 结果列表
 */
func (db *Db) ExecuteQueryByStatement(statement *SqlStatement) []any {
	if !statement.IsQuery {
		return nil
	}
	// 简化：假设单条 SQL，无参数
	return db.ExecuteQuery(statement.SqlList[0], [][]any{}, statement.ReturnType)
}

// ExecuteUpdateByStatement 使用 SqlStatement 执行更新
/**
 * 使用 SqlStatement 执行更新
 *
 * @param statement SQL 语句对象
 * @return int 影响行数
 */
func (db *Db) ExecuteUpdateByStatement(statement *SqlStatement) int {
	if statement.IsQuery {
		return 0
	}
	totalAffected := 0
	for _, sql := range statement.SqlList {
		result, err := db.DataSource.Exec(sql)
		if err != nil {
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, sql)
				if db.FaultTolerantMgr != nil {
					db.FaultTolerantMgr.RecordFailedOperation(&FailedOperation{
						Operation: "ExecuteUpdate",
						SQL:       sql,
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

// ExecuteOriginalUpdate 执行批量更新
/**
 * 执行批量更新
 *
 * @param sql SQL 语句
 * @param multiRowParams 多行参数
 * @return int 影响行数
 */
func (db *Db) ExecuteOriginalUpdate(sql string, multiRowParams [][]any) int {
	defer func() {
		if r := recover(); r != nil {
			LogError("批量更新发生 panic: %v, SQL=%s", r, sql)
		}
	}()
	totalAffected := 0
	for _, params := range multiRowParams {
		result, err := db.DataSource.Exec(sql, params...)
		if err != nil {
			if isConnectionError(err) {
				LogWarn("数据库连接已关闭或不可用: %v (SQL: %s)", err, sql)
				if db.FaultTolerantMgr != nil {
					db.FaultTolerantMgr.RecordFailedOperation(&FailedOperation{
						Operation: "ExecuteUpdate",
						SQL:       sql,
						Params:    toAnySlice(params),
						TableName: "",
					})
					db.FaultTolerantMgr.CheckAndReconnect()
				}
			} else {
				log.Printf("ExecuteOriginalUpdate error: %v", err)
			}
			continue
		}
		affected, _ := result.RowsAffected()
		totalAffected += int(affected)
	}
	return totalAffected
}

// ExecuteWithConnection 提供连接回调
/**
 * 提供直接使用 Connection 的回调入口
 *
 * @param fn 回调函数
 * @return error 执行错误
 */
func (db *Db) ExecuteWithConnection(fn func(*sql.Conn) error) error {
	conn, err := db.DataSource.Conn(context.TODO())
	if err != nil {
		return err
	}
	defer conn.Close()
	return fn(conn)
}

// ExecuteQuerySingle 单行查询
/**
 * 单行查询（带参数，返回非空结果，找不到返回类型默认值）
 *
 * @param sql SQL 语句
 * @param params 参数
 * @param returnType 返回类型
 * @return any 结果
 */
func (db *Db) ExecuteQuerySingle(sql string, params []any, returnType any) any {
	results := db.ExecuteQuery(sql, [][]any{params}, returnType)
	if len(results) > 0 {
		return results[0]
	}
	return getDefaultValue(returnType)
}

// ExecuteQuerySingleOrNull 单行查询，返回可空
/**
 * 单行查询（带参数，返回可空结果，找不到返回 null）
 *
 * @param sql SQL 语句
 * @param params 参数
 * @param returnType 返回类型
 * @return any 结果或 nil
 */
func (db *Db) ExecuteQuerySingleOrNull(sql string, params []any, returnType any) any {
	results := db.ExecuteQuery(sql, [][]any{params}, returnType)
	if len(results) > 0 {
		return results[0]
	}
	return nil
}

// Close 关闭数据库连接
/**
 * 关闭数据库连接
 *
 * @return error 关闭错误
 */
func (db *Db) Close() error {
	if db.FaultTolerantMgr != nil {
		db.FaultTolerantMgr.Stop()
	}
	return db.DataSource.Close()
}

/**
 * EnableFaultTolerance 启用容错管理器
 */
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

/**
 * DisableFaultTolerance 停用容错管理器
 */
func (db *Db) DisableFaultTolerance() {
	if db.FaultTolerantMgr == nil {
		return
	}
	db.FaultTolerantMgr.Stop()
	db.FaultTolerantMgr = nil
	LogInfo("容错管理器已停用")
}

/**
 * toAnySlice 将 []any 转为 []any
 */
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

/**
 * 获取类型的默认值
 *
 * @param t 类型
 * @return any 默认值
 */
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
