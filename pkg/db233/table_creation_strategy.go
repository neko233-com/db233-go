package db233

import (
	"reflect"
)

// 建表策略接口
type ITableCreationStrategy interface {
	// 获取数据库类型
	GetDatabaseType() EnumDatabaseType

	// 生成建表 SQL
	// tableName: 表名
	// entityType: 实体类型
	// uidColumn: 主键列名
	// 返回: SQL 语句
	// 返回: 错误
	GenerateCreateTableSQL(tableName string, entityType reflect.Type, uidColumn string) (string, error)

	// 获取 SQL 类型
	// field: 字段信息
	// 返回: SQL 类型字符串
	GetSQLType(field reflect.StructField) string

	// 检查表是否存在
	// db: 数据库连接
	// tableName: 表名
	// 返回: 是否存在
	// 返回: 错误
	TableExists(db *Db, tableName string) (bool, error)

	// 获取现有表的列信息
	// db: 数据库连接
	// tableName: 表名
	// 返回: 列名集合
	// 返回: 错误
	GetExistingColumns(db *Db, tableName string) (map[string]bool, error)

	// 获取表的所有列信息（包括类型、约束等）
	// db: 数据库连接
	// tableName: 表名
	// 返回: 列名到列信息的映射
	// 返回: 错误
	GetTableColumns(db *Db, tableName string) (map[string]ColumnInfo, error)

	// 生成添加列的 SQL（简化版本）
	// tableName: 表名
	// field: 字段信息
	// colName: 列名
	// 返回: ALTER TABLE ADD COLUMN SQL
	// 返回: 错误
	GenerateAddColumnSQL(tableName string, field reflect.StructField, colName string) (string, error)

	// 生成删除列的 SQL
	// tableName: 表名
	// colName: 列名
	// 返回: ALTER TABLE DROP COLUMN SQL
	// 返回: 错误
	GenerateDropColumnSQL(tableName string, colName string) (string, error)

	// 生成修改列的 SQL
	// tableName: 表名
	// field: 字段信息
	// colName: 列名
	// 返回: ALTER TABLE MODIFY COLUMN SQL
	// 返回: 错误
	GenerateModifyColumnSQL(tableName string, field reflect.StructField, colName string) (string, error)

	// 获取现有表的索引信息
	// db: 数据库连接
	// tableName: 表名
	// 返回: 索引名到索引信息的映射
	// 返回: 错误
	GetExistingIndexes(db *Db, tableName string) (map[string]*IndexMetaData, error)

	// 生成创建索引的 SQL
	// tableName: 表名
	// index: 索引元数据
	// 返回: CREATE INDEX SQL
	// 返回: 错误
	GenerateCreateIndexSQL(tableName string, index *IndexMetaData) (string, error)

	// 生成删除索引的 SQL
	// tableName: 表名
	// indexName: 索引名
	// 返回: DROP INDEX SQL
	// 返回: 错误
	GenerateDropIndexSQL(tableName string, indexName string) (string, error)
}

// ColumnInfo - 列信息
type ColumnInfo struct {
	Name            string
	Type            string
	IsNullable      bool
	IsPrimary       bool
	IsAutoIncrement bool
	Extra           string
	Default         any
}
