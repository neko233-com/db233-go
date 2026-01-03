package db233

/**
 * 数据库类型枚举
 *
 * @author neko233-com
 * @since 2026-01-04
 */
type DatabaseType string

const (
	// DatabaseTypeMySQL MySQL 数据库
	DatabaseTypeMySQL DatabaseType = "mysql"
	// DatabaseTypePostgreSQL PostgreSQL 数据库
	// TODO: PostgreSQL 支持将在未来版本中实现
	// DatabaseTypePostgreSQL DatabaseType = "postgresql"
)

/**
 * 获取数据库类型的字符串表示
 */
func (dt DatabaseType) String() string {
	return string(dt)
}

/**
 * 判断是否为有效的数据库类型
 */
func (dt DatabaseType) IsValid() bool {
	return dt == DatabaseTypeMySQL
	// TODO: PostgreSQL 支持将在未来版本中实现
	// return dt == DatabaseTypeMySQL || dt == DatabaseTypePostgreSQL
}

