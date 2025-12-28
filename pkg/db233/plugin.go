package db233

/**
 * Db233Plugin - Db233 插件接口
 *
 * 对应 Kotlin 版本的 AbstractDb233Plugin
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
type Db233Plugin interface {
	/**
	 * 获取插件名称
	 */
	GetPluginName() string

	/**
	 * 初始化插件
	 */
	InitPlugin()

	/**
	 * SQL 执行开始前的钩子
	 */
	Begin()

	/**
	 * 单条 SQL 执行前的钩子
	 */
	PreExecuteSql(context *ExecuteSqlContext)

	/**
	 * 单条 SQL 执行后的钩子
	 */
	PostExecuteSql(context *ExecuteSqlContext)

	/**
	 * SQL 执行结束后的钩子
	 */
	End()
}

/**
 * AbstractDb233Plugin - 插件抽象基类
 */
type AbstractDb233Plugin struct {
	PluginName string
}

/**
 * 创建插件
 */
func NewAbstractDb233Plugin(pluginName string) *AbstractDb233Plugin {
	return &AbstractDb233Plugin{
		PluginName: pluginName,
	}
}

/**
 * 获取插件名称
 */
func (p *AbstractDb233Plugin) GetPluginName() string {
	return p.PluginName
}

/**
 * 初始化插件（子类必须实现）
 */
func (p *AbstractDb233Plugin) InitPlugin() {
	// 子类实现
}

/**
 * SQL 执行开始前的钩子（默认空实现）
 */
func (p *AbstractDb233Plugin) Begin() {
	// 默认空实现
}

/**
 * 单条 SQL 执行前的钩子（默认空实现）
 */
func (p *AbstractDb233Plugin) PreExecuteSql(context *ExecuteSqlContext) {
	// 默认空实现
}

/**
 * 单条 SQL 执行后的钩子（默认空实现）
 */
func (p *AbstractDb233Plugin) PostExecuteSql(context *ExecuteSqlContext) {
	// 默认空实现
}

/**
 * SQL 执行结束后的钩子（默认空实现）
 */
func (p *AbstractDb233Plugin) End() {
	// 默认空实现
}

/**
 * 字符串表示
 */
func (p *AbstractDb233Plugin) String() string {
	return "Plugin(name='" + p.PluginName + "')"
}
