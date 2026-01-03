package db233

import (
	"reflect"
)

/**
 * 建表策略接口
 *
 * @author neko233-com
 * @since 2026-01-04
 */
type ITableCreationStrategy interface {
	/**
	 * 获取数据库类型
	 */
	GetDatabaseType() DatabaseType

	/**
	 * 生成建表 SQL
	 *
	 * @param tableName 表名
	 * @param entityType 实体类型
	 * @param uidColumn 主键列名
	 * @return SQL 语句
	 * @return 错误
	 */
	GenerateCreateTableSQL(tableName string, entityType reflect.Type, uidColumn string) (string, error)

	/**
	 * 获取 SQL 类型
	 *
	 * @param field 字段信息
	 * @return SQL 类型字符串
	 */
	GetSQLType(field reflect.StructField) string

	/**
	 * 检查表是否存在
	 *
	 * @param db 数据库连接
	 * @param tableName 表名
	 * @return 是否存在
	 * @return 错误
	 */
	TableExists(db *Db, tableName string) (bool, error)

	/**
	 * 获取现有表的列信息
	 *
	 * @param db 数据库连接
	 * @param tableName 表名
	 * @return 列名集合
	 * @return 错误
	 */
	GetExistingColumns(db *Db, tableName string) (map[string]bool, error)

	/**
	 * 生成添加列的 SQL
	 *
	 * @param tableName 表名
	 * @param colName 列名
	 * @param colType SQL 类型
	 * @param field 字段信息
	 * @param isPrimaryKey 是否为主键
	 * @return ALTER TABLE ADD COLUMN SQL
	 */
	GenerateAddColumnSQL(tableName string, colName string, colType string, field reflect.StructField, isPrimaryKey bool) string
}

