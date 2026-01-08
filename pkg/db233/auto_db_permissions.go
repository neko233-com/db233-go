package db233

/**
 * EnumAutoDbOperateType - 自动数据库操作类型枚举
 *
 * 控制自动迁移时允许的操作类型
 *
 * @author neko233-com
 * @since 2026-01-08
 */
type EnumAutoDbOperateType string

const (
	// AutoDbOperateCreateTable 创建表
	AutoDbOperateCreateTable EnumAutoDbOperateType = "CREATE_TABLE"

	// AutoDbOperateCreateColumn 创建列
	AutoDbOperateCreateColumn EnumAutoDbOperateType = "CREATE_COLUMN"

	// AutoDbOperateUpdateColumn 更新列（修改类型、约束等）
	AutoDbOperateUpdateColumn EnumAutoDbOperateType = "UPDATE_COLUMN"

	// AutoDbOperateDeleteColumn 删除列（危险操作，默认部分业务禁用）
	AutoDbOperateDeleteColumn EnumAutoDbOperateType = "DELETE_COLUMN"

	// AutoDbOperateCreateIndex 创建索引
	AutoDbOperateCreateIndex EnumAutoDbOperateType = "CREATE_INDEX"

	// AutoDbOperateDeleteIndex 删除索引
	AutoDbOperateDeleteIndex EnumAutoDbOperateType = "DELETE_INDEX"
)

/**
 * AutoDbPermissions - 自动数据库操作权限配置
 */
type AutoDbPermissions struct {
	// 允许的操作类型集合
	AllowedOperations map[EnumAutoDbOperateType]bool

	// 是否启用自动迁移
	EnableAutoMigration bool

	// 是否启用并发迁移
	EnableConcurrentMigration bool

	// 并发迁移的最大协程数
	MaxConcurrentWorkers int

	// 是否启用备份（在删除列前）
	EnableBackupBeforeDelete bool

	// 是否启用 Dry Run 模式（只记录，不执行）
	DryRun bool
}

/**
 * NewDefaultAutoDbPermissions 创建默认权限配置（全部开启）
 */
func NewDefaultAutoDbPermissions() *AutoDbPermissions {
	return &AutoDbPermissions{
		AllowedOperations: map[EnumAutoDbOperateType]bool{
			AutoDbOperateCreateTable:  true,
			AutoDbOperateCreateColumn: true,
			AutoDbOperateUpdateColumn: true,
			AutoDbOperateDeleteColumn: true, // 默认开启
			AutoDbOperateCreateIndex:  true,
			AutoDbOperateDeleteIndex:  true,
		},
		EnableAutoMigration:       true,
		EnableConcurrentMigration: true,
		MaxConcurrentWorkers:      10, // 默认10个并发协程
		EnableBackupBeforeDelete:  false,
		DryRun:                    false,
	}
}

/**
 * NewSafeAutoDbPermissions 创建安全权限配置（禁用删除列）
 */
func NewSafeAutoDbPermissions() *AutoDbPermissions {
	return &AutoDbPermissions{
		AllowedOperations: map[EnumAutoDbOperateType]bool{
			AutoDbOperateCreateTable:  true,
			AutoDbOperateCreateColumn: true,
			AutoDbOperateUpdateColumn: true,
			AutoDbOperateDeleteColumn: false, // 禁用删除列
			AutoDbOperateCreateIndex:  true,
			AutoDbOperateDeleteIndex:  false, // 禁用删除索引
		},
		EnableAutoMigration:       true,
		EnableConcurrentMigration: true,
		MaxConcurrentWorkers:      10,
		EnableBackupBeforeDelete:  true,
		DryRun:                    false,
	}
}

/**
 * IsAllowed 检查操作是否被允许
 */
func (p *AutoDbPermissions) IsAllowed(operationType EnumAutoDbOperateType) bool {
	if !p.EnableAutoMigration {
		return false
	}
	allowed, exists := p.AllowedOperations[operationType]
	return exists && allowed
}

/**
 * SetAllowed 设置操作权限
 */
func (p *AutoDbPermissions) SetAllowed(operationType EnumAutoDbOperateType, allowed bool) {
	p.AllowedOperations[operationType] = allowed
}

/**
 * DisableDeleteColumn 禁用删除列操作
 */
func (p *AutoDbPermissions) DisableDeleteColumn() {
	p.AllowedOperations[AutoDbOperateDeleteColumn] = false
}

/**
 * EnableDeleteColumn 启用删除列操作
 */
func (p *AutoDbPermissions) EnableDeleteColumn() {
	p.AllowedOperations[AutoDbOperateDeleteColumn] = true
}
