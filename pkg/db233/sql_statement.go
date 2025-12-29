package db233

/**
 * SQL 语句装配对象 - Go 版
 *
 * 用途：封装一次 SQL 执行所需的所有信息
 *
 * 说明：
 * - 对应 Kotlin 版本的 SqlStatement
 * - 使用 interface{} 表示返回类型
 * - 支持查询和更新语句
 *
 * 使用示例：
 * ```go
 * // 创建查询语句
 * stmt := NewQueryStatement("SELECT * FROM user", User{})
 *
 * // 创建更新语句
 * stmt := NewUpdateStatement("UPDATE user SET name = ?")
 * ```
 *
 * @author neko233-com
 * @since 2025-12-28
 */
type SqlStatement struct {
	// IsQuery 是否为查询语句（SELECT），默认 false
	IsQuery bool

	// IsAutoCommit 是否自动提交事务，默认 true
	IsAutoCommit bool

	// SqlList SQL 语句列表（支持批量执行）
	SqlList []string

	// ReturnType 返回结果的类型（用于 ORM 映射）
	ReturnType interface{}
}

/**
 * 创建查询语句（单条 SQL）
 *
 * @param sql SQL 语句
 * @param returnType 返回类型
 * @return SqlStatement 实例
 */
func NewQueryStatement(sql string, returnType interface{}) *SqlStatement {
	return &SqlStatement{
		IsQuery:      true,
		IsAutoCommit: true,
		SqlList:      []string{sql},
		ReturnType:   returnType,
	}
}

/**
 * 创建批量查询语句
 *
 * @param sqlList SQL 语句列表
 * @param returnType 返回类型
 * @return SqlStatement 实例
 */
func NewQueryStatements(sqlList []string, returnType interface{}) *SqlStatement {
	return &SqlStatement{
		IsQuery:      true,
		IsAutoCommit: true,
		SqlList:      sqlList,
		ReturnType:   returnType,
	}
}

// NewUpdateStatement 创建更新语句
/**
 * 创建更新语句（单条 SQL）
 *
 * @param sql SQL 语句
 * @return SqlStatement 实例
 */
func NewUpdateStatement(sql string) *SqlStatement {
	return &SqlStatement{
		IsQuery:      false,
		IsAutoCommit: true,
		SqlList:      []string{sql},
		ReturnType:   nil,
	}
}

// NewUpdateStatements 创建批量更新语句
/**
 * 创建批量更新语句
 *
 * @param sqlList SQL 语句列表
 * @return SqlStatement 实例
 */
func NewUpdateStatements(sqlList []string) *SqlStatement {
	return &SqlStatement{
		IsQuery:      false,
		IsAutoCommit: true,
		SqlList:      sqlList,
		ReturnType:   nil,
	}
}
