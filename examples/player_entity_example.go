package examples

import (
	"fmt"
	"time"

	db233 "github.com/neko233-com/db233-go/pkg/db233"
)

/**
 * ============================================================
 * 示例：类似 JPA 的实体继承机制
 *
 * 功能演示：
 * 1. 基础实体类定义（父类）
 * 2. 业务实体类继承（子类）
 * 3. 自动主键检测
 * 4. UPSERT 自动处理
 * 5. 字段忽略机制
 * ============================================================
 */

// ============================================================
// 第一层：基础实体（最顶层父类）
// ============================================================

// BaseEntity 所有实体的基类
// 类似 JPA 的 @MappedSuperclass
type BaseEntity struct {
	// 创建时间（所有实体都需要）
	CreatedAt time.Time `json:"createdAt" db:"created_at"`

	// 更新时间（所有实体都需要）
	UpdatedAt time.Time `json:"updatedAt" db:"updated_at"`
}

// BeforeSaveToDb 保存前的钩子（所有子类自动继承）
func (b *BaseEntity) BeforeSaveToDb() {
	now := time.Now()
	if b.CreatedAt.IsZero() {
		b.CreatedAt = now
	}
	b.UpdatedAt = now
}

// AfterLoadFromDb 加载后的钩子（所有子类自动继承）
func (b *BaseEntity) AfterLoadFromDb() {
	// 可以在这里进行数据验证或转换
}

// ============================================================
// 第二层：玩家基础实体（游戏业务基类）
// ============================================================

// BasePlayerEntity 玩家实体基类
// 所有玩家相关的数据表都继承此类
// 类似 Kotlin: abstract class AbstractPlayerEntity
type BasePlayerEntity struct {
	BaseEntity // 嵌入 BaseEntity，继承创建时间和更新时间

	// 玩家ID（主键）
	// 注意：使用小驼峰命名兼容 Kotlin JPA 生成的列名
	PlayerID int64 `json:"playerId" db:"playerId,primary_key"`
}

// ========== 业务方法（所有子类自动继承） ==========

// GetPlayerID 获取玩家ID
func (b *BasePlayerEntity) GetPlayerID() int64 {
	return b.PlayerID
}

// SetPlayerID 设置玩家ID
func (b *BasePlayerEntity) SetPlayerID(playerID int64) {
	b.PlayerID = playerID
}

// IsValid 验证数据有效性
func (b *BasePlayerEntity) IsValid() bool {
	return b.PlayerID > 0
}

// ============================================================
// 第三层：具体业务实体（继承玩家基类）
// ============================================================

// StrengthEntity 力量系统实体
// 存储玩家的力量值、训练记录等
type StrengthEntity struct {
	BasePlayerEntity // 嵌入父类，自动继承 playerId + created_at + updated_at

	// ========== 业务字段 ==========

	// 当前力量值
	CurrentStrength int `json:"currentStrength" db:"current_strength"`

	// 最大力量值
	MaxStrength int `json:"maxStrength" db:"max_strength"`

	// 最后更新时间戳（毫秒）
	LastUpdateTimeMs int64 `json:"lastUpdateTimeMs" db:"last_update_time_ms"`

	// ========== 忽略字段（不存储到数据库） ==========

	// 缓存值（运行时计算，不存储）
	cachedPowerLevel float64 `db:"-"`

	// 临时标记（不需要持久化）
	isDirty bool
}

// ========== 实现 IDbEntity 接口 ==========

// TableName 返回表名
func (e *StrengthEntity) TableName() string {
	return "StrengthEntity"
}

// SerializeBeforeSaveDb 保存前序列化
func (e *StrengthEntity) SerializeBeforeSaveDb() {
	// 调用父类钩子（自动更新时间戳）
	e.BeforeSaveToDb()

	// 业务逻辑：更新最后修改时间
	e.LastUpdateTimeMs = time.Now().UnixMilli()
}

// DeserializeAfterLoadDb 加载后反序列化
func (e *StrengthEntity) DeserializeAfterLoadDb() {
	// 调用父类钩子
	e.AfterLoadFromDb()

	// 业务逻辑：计算缓存值
	e.cachedPowerLevel = float64(e.CurrentStrength) / float64(e.MaxStrength) * 100
}

// ========== 业务方法 ==========

// AddStrength 增加力量值
func (e *StrengthEntity) AddStrength(amount int) {
	e.CurrentStrength += amount
	if e.CurrentStrength > e.MaxStrength {
		e.CurrentStrength = e.MaxStrength
	}
	e.isDirty = true
}

// GetPowerLevel 获取力量等级（百分比）
func (e *StrengthEntity) GetPowerLevel() float64 {
	return e.cachedPowerLevel
}

// ============================================================
// InventoryEntity 背包系统实体（另一个继承示例）
// ============================================================

// InventoryEntity 玩家背包实体
type InventoryEntity struct {
	BasePlayerEntity // 同样继承玩家基类

	// 物品ID
	ItemID int64 `json:"itemId" db:"item_id"`

	// 物品数量
	Quantity int `json:"quantity" db:"quantity"`

	// 获取时间
	ObtainedAt time.Time `json:"obtainedAt" db:"obtained_at"`
}

// TableName 返回表名
func (i *InventoryEntity) TableName() string {
	return "InventoryEntity"
}

// SerializeBeforeSaveDb 保存前序列化
func (i *InventoryEntity) SerializeBeforeSaveDb() {
	i.BeforeSaveToDb()
}

// DeserializeAfterLoadDb 加载后反序列化
func (i *InventoryEntity) DeserializeAfterLoadDb() {
	i.AfterLoadFromDb()
}

// ============================================================
// 示例：如何使用
// ============================================================

// ExamplePlayerEntityUsage 演示如何使用玩家实体
func ExamplePlayerEntityUsage() {
	fmt.Println("========== DB233 JPA 风格实体继承示例 ==========\n")

	// 假设已经创建了数据库连接
	// db := db233.NewDb(dataSource, 0, nil)

	fmt.Println("1. 创建实体（子类自动拥有父类字段）")
	entity := &StrengthEntity{
		BasePlayerEntity: BasePlayerEntity{
			BaseEntity: BaseEntity{
				// created_at 和 updated_at 会在 BeforeSaveToDb() 中自动设置
			},
			PlayerID: 1000022, // 主键（自动检测）
		},
		CurrentStrength:  100,
		MaxStrength:      500,
		LastUpdateTimeMs: time.Now().UnixMilli(),
	}

	fmt.Printf("   玩家ID: %d\n", entity.GetPlayerID())
	fmt.Printf("   当前力量: %d\n", entity.CurrentStrength)
	fmt.Printf("   数据有效: %v\n\n", entity.IsValid())

	// 2. 自动创建表（支持嵌入结构体）
	fmt.Println("2. 自动创建表")
	fmt.Println("   生成的 SQL 会包含:")
	fmt.Println("   - playerId BIGINT PRIMARY KEY")
	fmt.Println("   - created_at TIMESTAMP")
	fmt.Println("   - updated_at TIMESTAMP")
	fmt.Println("   - current_strength INT")
	fmt.Println("   - max_strength INT")
	fmt.Println("   - last_update_time_ms BIGINT\n")

	// cm := db233.GetCrudManagerInstance()
	// cm.AutoMigrateTableSimple(db, &StrengthEntity{})

	// 3. 保存实体（UPSERT 自动处理）
	fmt.Println("3. 保存实体（UPSERT）")
	// repo := db233.NewBaseCrudRepository(db)
	// repo.Save(entity) // 第一次 INSERT
	fmt.Println("   第一次 Save: INSERT（主键不存在）\n")

	// 4. 更新实体
	fmt.Println("4. 更新实体（自动变为 UPDATE）")
	entity.AddStrength(50)
	// repo.Save(entity) // 第二次自动变为 UPDATE
	fmt.Printf("   力量值: %d -> %d\n", 100, entity.CurrentStrength)
	fmt.Println("   第二次 Save: UPDATE（主键已存在，不会报错）\n")

	// 5. 查询实体
	fmt.Println("5. 查询实体")
	// found, _ := repo.FindById(int64(1000022), &StrengthEntity{})
	// foundEntity := found.(*StrengthEntity)
	fmt.Println("   自动调用 DeserializeAfterLoadDb()")
	fmt.Println("   自动计算 cachedPowerLevel\n")

	// 6. 批量操作
	fmt.Println("6. 批量操作示例")
	entities := []*StrengthEntity{
		{BasePlayerEntity: BasePlayerEntity{PlayerID: 1001}, CurrentStrength: 100, MaxStrength: 500},
		{BasePlayerEntity: BasePlayerEntity{PlayerID: 1002}, CurrentStrength: 200, MaxStrength: 500},
		{BasePlayerEntity: BasePlayerEntity{PlayerID: 1003}, CurrentStrength: 300, MaxStrength: 500},
	}
	fmt.Printf("   批量保存 %d 个实体\n\n", len(entities))

	fmt.Println("========== 优势对比 ==========")
	fmt.Println("✅ 无需手动实现 GetDbUid() 方法")
	fmt.Println("✅ 子类自动继承父类的所有字段和方法")
	fmt.Println("✅ 自动处理 UPSERT，避免主键冲突")
	fmt.Println("✅ 支持多层继承（BaseEntity -> BasePlayerEntity -> StrengthEntity）")
	fmt.Println("✅ 字段忽略机制（db:\"-\" 或无 db tag）")
	fmt.Println("✅ 线程安全的元数据缓存\n")

	fmt.Println("========== 完整示例结束 ==========")
}

// ============================================================
// 主函数（如果直接运行此文件）
// ============================================================

/*
func main() {
	ExamplePlayerEntityUsage()
}
*/
