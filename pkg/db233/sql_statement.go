package db233

// SQL 语句装配对象 - Go 版
// 用途：封装一次 SQL 执行所需的所有信息
// 说明：
// - 对应 Kotlin 版本的 SqlStatement
// - 使用 any 表示返回类型
// - 支持查询和更新语句
// 使用示例：
// ```go
// 创建查询语句
// stmt := NewQueryStatement("SELECT * FROM user", User{})
// 创建更新语句
// stmt := NewUpdateStatement("UPDATE user SET name = ?")
// ```
type SqlStatement struct {
	// IsQuery 是否为查询语句（SELECT），默认 false
	IsQuery bool

	// IsAutoCommit 是否自动提交事务，默认 true
	IsAutoCommit bool

	// SqlList SQL 语句列表（支持批量执行）
	SqlList []string

	// ReturnType 返回结果的类型（用于 ORM 映射）
	ReturnType any
}

// 创建查询语句（单条 SQL）
// sql: SQL 语句
// returnType: 返回类型
// 返回: SqlStatement 实例
func NewQueryStatement(sql string, returnType any) *SqlStatement {
	return &SqlStatement{
		IsQuery:      true,
		IsAutoCommit: true,
		SqlList:      []string{sql},
		ReturnType:   returnType,
	}
}

// 创建批量查询语句
// sqlList: SQL 语句列表
// returnType: 返回类型
// 返回: SqlStatement 实例
func NewQueryStatements(sqlList []string, returnType any) *SqlStatement {
	return &SqlStatement{
		IsQuery:      true,
		IsAutoCommit: true,
		SqlList:      append([]string(nil), sqlList...),
		ReturnType:   returnType,
	}
}

// NewUpdateStatement 创建更新语句
// 创建更新语句（单条 SQL）
// sql: SQL 语句
// 返回: SqlStatement 实例
func NewUpdateStatement(sql string) *SqlStatement {
	return &SqlStatement{
		IsQuery:      false,
		IsAutoCommit: true,
		SqlList:      []string{sql},
		ReturnType:   nil,
	}
}

// NewUpdateStatements 创建批量更新语句
// 创建批量更新语句
// sqlList: SQL 语句列表
// 返回: SqlStatement 实例
func NewUpdateStatements(sqlList []string) *SqlStatement {
	return &SqlStatement{
		IsQuery:      false,
		IsAutoCommit: true,
		SqlList:      append([]string(nil), sqlList...),
		ReturnType:   nil,
	}
}
