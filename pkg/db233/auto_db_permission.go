package db233

/**
 * EnumAutoDbOperateType - 自动数据库操作类型
 *
 * 定义自动创建表时允许的操作类型
 *
 * @author neko233-com
 * @since 2026-01-08
 */
type EnumAutoDbOperateType string

const (
	// EnumAutoDbOperateTypeCreateColumn 创建列
	EnumAutoDbOperateTypeCreateColumn EnumAutoDbOperateType = "CREATE_COLUMN"
	// EnumAutoDbOperateTypeUpdateColumn 更新列
	EnumAutoDbOperateTypeUpdateColumn EnumAutoDbOperateType = "UPDATE_COLUMN"
	// EnumAutoDbOperateTypeDeleteColumn 删除列
	EnumAutoDbOperateTypeDeleteColumn EnumAutoDbOperateType = "DELETE_COLUMN"
)

/**
 * AutoDbPermission - 自动数据库操作权限配置
 *
 * 控制自动创建表/修改表结构时允许的操作
 * 默认开启所有操作，但 DeleteColumn 在生产环境建议关闭
 *
 * @author neko233-com
 * @since 2026-01-08
 */
type AutoDbPermission struct {
	// 允许的操作类型集合
	AllowedOperations map[EnumAutoDbOperateType]bool
}

/**
 * NewDefaultAutoDbPermission 创建默认权限配置（开启所有操作）
 */
func NewDefaultAutoDbPermission() *AutoDbPermission {
	return &AutoDbPermission{
		AllowedOperations: map[EnumAutoDbOperateType]bool{
			EnumAutoDbOperateTypeCreateColumn: true,
			EnumAutoDbOperateTypeUpdateColumn: true,
			EnumAutoDbOperateTypeDeleteColumn: true,
		},
	}
}

/**
 * NewSafeAutoDbPermission 创建安全权限配置（不允许删除列）
 */
func NewSafeAutoDbPermission() *AutoDbPermission {
	return &AutoDbPermission{
		AllowedOperations: map[EnumAutoDbOperateType]bool{
			EnumAutoDbOperateTypeCreateColumn: true,
			EnumAutoDbOperateTypeUpdateColumn: true,
			EnumAutoDbOperateTypeDeleteColumn: false, // 生产环境建议关闭
		},
	}
}

/**
 * IsAllowed 检查操作是否被允许
 */
func (p *AutoDbPermission) IsAllowed(operationType EnumAutoDbOperateType) bool {
	if p == nil || p.AllowedOperations == nil {
		// 默认允许所有操作
		return true
	}
	allowed, exists := p.AllowedOperations[operationType]
	return exists && allowed
}

/**
 * SetAllowed 设置操作权限
 */
func (p *AutoDbPermission) SetAllowed(operationType EnumAutoDbOperateType, allowed bool) {
	if p.AllowedOperations == nil {
		p.AllowedOperations = make(map[EnumAutoDbOperateType]bool)
	}
	p.AllowedOperations[operationType] = allowed
}

/**
 * DisableDeleteColumn 禁用删除列操作（推荐用于生产环境）
 */
func (p *AutoDbPermission) DisableDeleteColumn() {
	p.SetAllowed(EnumAutoDbOperateTypeDeleteColumn, false)
}

/**
 * EnableAllOperations 启用所有操作
 */
func (p *AutoDbPermission) EnableAllOperations() {
	p.SetAllowed(EnumAutoDbOperateTypeCreateColumn, true)
	p.SetAllowed(EnumAutoDbOperateTypeUpdateColumn, true)
	p.SetAllowed(EnumAutoDbOperateTypeDeleteColumn, true)
}
