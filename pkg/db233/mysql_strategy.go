package db233

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

/**
 * MySQL 建表策略
 *
 * @author neko233-com
 * @since 2026-01-04
 */
type MySQLStrategy struct {
	cm *CrudManager
}

/**
 * 创建 MySQL 策略实例
 */
func NewMySQLStrategy(cm *CrudManager) *MySQLStrategy {
	return &MySQLStrategy{cm: cm}
}

/**
 * 获取数据库类型
 */
func (s *MySQLStrategy) GetDatabaseType() DatabaseType {
	return DatabaseTypeMySQL
}

/**
 * 生成建表 SQL
 */
func (s *MySQLStrategy) GenerateCreateTableSQL(tableName string, entityType reflect.Type, uidColumn string) (string, error) {
	if tableName == "" {
		return "", NewDb233Exception("无法获取表名")
	}

	var columns []string
	var primaryKeys []string

	for i := 0; i < entityType.NumField(); i++ {
		field := entityType.Field(i)
		if !field.IsExported() {
			LogDebug("跳过未导出字段: 表=%s, 字段=%s", tableName, field.Name)
			continue
		}

		dbTag := field.Tag.Get("db")
		// 跳过明确标记为忽略的字段
		if dbTag == "-" {
			LogDebug("跳过标记为忽略的字段: 表=%s, 字段=%s", tableName, field.Name)
			continue
		}
		// 跳过明确标记为 skip 的字段
		if strings.Contains(dbTag, "skip") {
			LogDebug("跳过标记为 skip 的字段: 表=%s, 字段=%s", tableName, field.Name)
			continue
		}

		// 获取列名（支持没有 db 标签的字段）
		colName := s.cm.GetColumnName(field)
		if colName == "" {
			LogDebug("跳过无法确定列名的字段: 表=%s, 字段=%s", tableName, field.Name)
			continue
		}

		// 获取 SQL 类型
		colType := s.GetSQLType(field)
		colDef := fmt.Sprintf("`%s` %s", colName, colType)

		// 检查是否自增
		if strings.Contains(dbTag, "auto_increment") {
			colDef += " AUTO_INCREMENT"
		}

		// 判断是否为主键
		isPrimaryKey := s.cm.IsPrimaryKey(field)
		// 如果指定了 uidColumn，且当前字段名匹配，也认为是主键
		if uidColumn != "" && colName == uidColumn {
			isPrimaryKey = true
		}

		// 默认允许为 NULL，除非明确标记为 not_null 或是主键
		// 主键必须为 NOT NULL（数据库要求）
		if strings.Contains(dbTag, "not_null") || isPrimaryKey {
			colDef += " NOT NULL"
		} else {
			colDef += " NULL"
		}

		columns = append(columns, colDef)

		if isPrimaryKey {
			primaryKeys = append(primaryKeys, fmt.Sprintf("`%s`", colName))
		}
	}

	if len(primaryKeys) > 0 {
		columns = append(columns, fmt.Sprintf("PRIMARY KEY (%s)", strings.Join(primaryKeys, ", ")))
	}

	if len(columns) == 0 {
		return "", NewDb233Exception(fmt.Sprintf("表 %s 没有可用的列", tableName))
	}

	createSQL := fmt.Sprintf("CREATE TABLE `%s` (\n\t%s\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci", tableName, strings.Join(columns, ",\n\t"))

	LogDebug("生成 MySQL 建表SQL: 表=%s, SQL=%s", tableName, createSQL)
	return createSQL, nil
}

/**
 * 获取 SQL 类型
 */
func (s *MySQLStrategy) GetSQLType(field reflect.StructField) string {
	fieldType := field.Type

	// 优先检查 db_type tag（用于指定数据库类型，如 TEXT）
	if dbTypeTag := field.Tag.Get("db_type"); dbTypeTag != "" {
		return dbTypeTag
	}

	// 其次检查 type tag（向后兼容）
	if typeTag := field.Tag.Get("type"); typeTag != "" {
		return typeTag
	}

	// 处理指针类型
	kind := fieldType.Kind()
	if kind == reflect.Ptr {
		fieldType = fieldType.Elem()
		kind = fieldType.Kind()
	}

	// 检查是否为复杂类型（map, slice, array），需要序列化为 JSON，使用 TEXT 类型
	if s.isComplexTypeForSQL(kind, fieldType) {
		LogDebug("检测到复杂类型字段，使用 TEXT 类型: 字段=%s, 类型=%s", field.Name, fieldType.String())
		return "TEXT"
	}

	switch kind {
	case reflect.Int, reflect.Int32:
		return "INT"
	case reflect.Int8:
		return "TINYINT"
	case reflect.Int16:
		return "SMALLINT"
	case reflect.Int64:
		return "BIGINT"
	case reflect.Uint, reflect.Uint32:
		return "INT UNSIGNED"
	case reflect.Uint8:
		return "TINYINT UNSIGNED"
	case reflect.Uint16:
		return "SMALLINT UNSIGNED"
	case reflect.Uint64:
		return "BIGINT UNSIGNED"
	case reflect.Float32:
		return "FLOAT"
	case reflect.Float64:
		return "DOUBLE"
	case reflect.String:
		size := 255
		if sizeTag := field.Tag.Get("size"); sizeTag != "" {
			if s, err := strconv.Atoi(sizeTag); err == nil {
				size = s
			}
		}
		// 如果 size 很大，使用 TEXT
		if size > 65535 {
			return "TEXT"
		}
		return fmt.Sprintf("VARCHAR(%d)", size)
	case reflect.Bool:
		return "TINYINT(1)"
	case reflect.Struct:
		if fieldType == reflect.TypeOf(time.Time{}) {
			return "TIMESTAMP"
		}
		// 其他结构体类型，使用 TEXT（需要序列化）
		LogDebug("检测到结构体类型字段，使用 TEXT 类型: 字段=%s, 类型=%s", field.Name, fieldType.String())
		return "TEXT"
	}

	return "VARCHAR(255)"
}

/**
 * 判断是否为复杂类型（用于 SQL 类型判断）
 */
func (s *MySQLStrategy) isComplexTypeForSQL(kind reflect.Kind, fieldType reflect.Type) bool {
	switch kind {
	case reflect.Map, reflect.Slice, reflect.Array:
		return true
	case reflect.Struct:
		// time.Time 是数据库原生支持的类型，不需要序列化
		if fieldType == reflect.TypeOf(time.Time{}) {
			return false
		}
		// 其他结构体需要序列化
		return true
	default:
		return false
	}
}

/**
 * 检查表是否存在
 */
func (s *MySQLStrategy) TableExists(db *Db, tableName string) (bool, error) {
	query := "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?"
	row := db.DataSource.QueryRow(query, tableName)

	var count int
	err := row.Scan(&count)
	if err != nil {
		return false, NewQueryExceptionWithCause(err, "检查表存在性失败")
	}

	return count > 0, nil
}

/**
 * 获取现有表的列信息
 */
func (s *MySQLStrategy) GetExistingColumns(db *Db, tableName string) (map[string]bool, error) {
	query := "SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?"
	rows, err := db.DataSource.Query(query, tableName)
	if err != nil {
		return nil, NewQueryExceptionWithCause(err, "获取表列信息失败")
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var colName string
		if err := rows.Scan(&colName); err != nil {
			return nil, NewQueryExceptionWithCause(err, "扫描列名失败")
		}
		columns[colName] = true
	}

	return columns, nil
}

/**
 * 生成添加列的 SQL
 */
func (s *MySQLStrategy) GenerateAddColumnSQL(tableName string, colName string, colType string, field reflect.StructField, isPrimaryKey bool) string {
	dbTag := field.Tag.Get("db")
	colDef := fmt.Sprintf("ADD COLUMN `%s` %s", colName, colType)

	// 检查是否自增
	if strings.Contains(dbTag, "auto_increment") {
		colDef += " AUTO_INCREMENT"
	}

	// 默认允许为 NULL，除非明确标记为 not_null 或是主键
	// 主键必须为 NOT NULL（数据库要求）
	if strings.Contains(dbTag, "not_null") || isPrimaryKey {
		colDef += " NOT NULL"
	} else {
		colDef += " NULL"
	}

	return fmt.Sprintf("ALTER TABLE `%s` %s", tableName, colDef)
}

