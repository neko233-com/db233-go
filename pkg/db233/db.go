package db233

import (
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
	 * @return []interface{} 结果列表
	 */
	ExecuteQuery(sql string, paramsArray [][]interface{}, returnType interface{}) []interface{}

	/**
	 * 使用 SqlStatement 执行查询
	 *
	 * @param statement SQL 语句对象
	 * @return []interface{} 结果列表
	 */
	ExecuteQueryByStatement(statement *SqlStatement) []interface{}

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
	ExecuteOriginalUpdate(sql string, multiRowParams [][]interface{}) int

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
	DataSource *sql.DB
	DbId       int
	DbGroup    *DbGroup
	DatabaseType DatabaseType // 数据库类型，默认为 MySQL
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
		DataSource: dataSource,
		DbId:       dbId,
		DbGroup:    dbGroup,
		DatabaseType: DatabaseTypeMySQL, // 默认 MySQL
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
func NewDbWithType(dataSource *sql.DB, dbId int, dbGroup *DbGroup, dbType DatabaseType) *Db {
	if dbType == "" || !dbType.IsValid() {
		dbType = DatabaseTypeMySQL
	}
	return &Db{
		DataSource: dataSource,
		DbId:       dbId,
		DbGroup:    dbGroup,
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
 * @return []interface{} 结果列表
 */
func (db *Db) ExecuteQuery(sql string, paramsArray [][]interface{}, returnType interface{}) []interface{} {
	var results []interface{}
	for _, params := range paramsArray {
		rows, err := db.DataSource.Query(sql, params...)
		if err != nil {
			log.Printf("ExecuteQuery error: %v", err)
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
 * @return []interface{} 结果列表
 */
func (db *Db) ExecuteQueryByStatement(statement *SqlStatement) []interface{} {
	if !statement.IsQuery {
		return nil
	}
	// 简化：假设单条 SQL，无参数
	return db.ExecuteQuery(statement.SqlList[0], [][]interface{}{}, statement.ReturnType)
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
			log.Printf("ExecuteUpdate error: %v", err)
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
func (db *Db) ExecuteOriginalUpdate(sql string, multiRowParams [][]interface{}) int {
	totalAffected := 0
	for _, params := range multiRowParams {
		result, err := db.DataSource.Exec(sql, params...)
		if err != nil {
			log.Printf("ExecuteOriginalUpdate error: %v", err)
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
	conn, err := db.DataSource.Conn(nil)
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
 * @return interface{} 结果
 */
func (db *Db) ExecuteQuerySingle(sql string, params []interface{}, returnType interface{}) interface{} {
	results := db.ExecuteQuery(sql, [][]interface{}{params}, returnType)
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
 * @return interface{} 结果或 nil
 */
func (db *Db) ExecuteQuerySingleOrNull(sql string, params []interface{}, returnType interface{}) interface{} {
	results := db.ExecuteQuery(sql, [][]interface{}{params}, returnType)
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
	return db.DataSource.Close()
}

/**
 * 获取类型的默认值
 *
 * @param t 类型
 * @return interface{} 默认值
 */
func getDefaultValue(t interface{}) interface{} {
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
