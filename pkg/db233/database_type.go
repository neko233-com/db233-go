package db233

/**
 * 数据库类型枚举
 *
 * @author neko233-com
 * @since 2026-01-04
 */
type EnumDatabaseType string

const (
	// EnumDatabaseTypeMySQL MySQL 数据库
	EnumDatabaseTypeMySQL EnumDatabaseType = "mysql"
	// EnumDatabaseTypePostgreSQL PostgreSQL 数据库
	EnumDatabaseTypePostgreSQL EnumDatabaseType = "postgresql"
)

/**
 * 获取数据库类型的字符串表示
 */
func (dt EnumDatabaseType) String() string {
	return string(dt)
}

/**
 * 判断是否为有效的数据库类型
 */
func (dt EnumDatabaseType) IsValid() bool {
	return dt == EnumDatabaseTypeMySQL || dt == EnumDatabaseTypePostgreSQL
}
