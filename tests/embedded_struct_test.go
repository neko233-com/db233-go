package tests

import (
	"testing"

	db233 "github.com/neko233-com/db233-go/pkg/db233"
)

// =====================================================
// BasePlayerEntity 基础玩家实体
// 提供默认实现，业务实体可以嵌入此结构体
// =====================================================

type BasePlayerEntity struct {
	// PlayerID 玩家ID（主键，Long 类型，参考 Kotlin）
	// 注意：db tag 使用小驼峰格式，兼容 Kotlin JPA 生成的列名
	PlayerID int64 `json:"playerId" db:"playerId" primary_key:"true"`
}

// GetPlayerID 获取玩家ID
func (b *BasePlayerEntity) GetPlayerID() int64 {
	return b.PlayerID
}

// SetPlayerID 设置玩家ID
func (b *BasePlayerEntity) SetPlayerID(playerID int64) {
	b.PlayerID = playerID
}

// AfterLoadFromDb 从数据库加载后的回调（默认空实现）
func (b *BasePlayerEntity) AfterLoadFromDb() {}

// BeforeSaveToDb 保存到数据库前的回调（默认空实现）
func (b *BasePlayerEntity) BeforeSaveToDb() {}

// =====================================================
// StrengthEntity 力量实体（嵌入 BasePlayerEntity）
// =====================================================

type StrengthEntity struct {
	BasePlayerEntity        // 嵌入基础实体（包含 playerId 主键）
	LastUpdateTimeMs int64  `json:"lastUpdateTimeMs" db:"last_update_time_ms"`
	CurrentStrength  int    `json:"currentStrength" db:"current_strength"`
	UpdatedAtTimeMs  int64  `json:"updatedAtTimeMs" db:"updated_at_time_ms"`
	ShouldBeIgnored  string `json:"-" db:"-"` // 应该被忽略的字段
	NoDbTag          string `json:"noDbTag"`  // 没有 db tag，应该被忽略
}

// TableName 实现 IDbEntity 接口
func (e *StrengthEntity) TableName() string {
	return "StrengthEntity"
}

// SerializeBeforeSaveDb 实现 IDbEntity 接口
func (e *StrengthEntity) SerializeBeforeSaveDb() {
	e.BeforeSaveToDb()
}

// DeserializeAfterLoadDb 实现 IDbEntity 接口
func (e *StrengthEntity) DeserializeAfterLoadDb() {
	e.AfterLoadFromDb()
}

func TestEmbeddedStructSupport(t *testing.T) {
	// 创建数据库连接
	db := CreateTestDb(t)
	if db == nil {
		t.Skip("无法创建测试数据库连接")
		return
	}
	defer db.DataSource.Close()

	// 清理旧表（确保测试环境干净）
	_, _ = db.DataSource.Exec("DROP TABLE IF EXISTS StrengthEntity")

	// Clear cache to ensure fresh scan
	cm := db233.GetCrudManagerInstance()
	cm.ClearPrimaryKeyCache()

	// 自动创建表
	err := cm.AutoMigrateTableSimple(db, &StrengthEntity{})
	if err != nil {
		t.Fatalf("自动创建表失败: %v", err)
	}

	// 创建 CRUD Repository
	crudRepo := db233.NewBaseCrudRepository(db)

	// 测试保存（UPSERT）
	entity := &StrengthEntity{
		BasePlayerEntity: BasePlayerEntity{
			PlayerID: 1000022,
		},
		LastUpdateTimeMs: 1234567890,
		CurrentStrength:  100,
		UpdatedAtTimeMs:  1234567890,
		ShouldBeIgnored:  "This should not be saved",
		NoDbTag:          "This should also not be saved",
	}

	// 第一次保存（INSERT）
	err = crudRepo.Save(entity)
	if err != nil {
		t.Fatalf("第一次保存失败: %v", err)
	}
	t.Logf("第一次保存成功: PlayerID=%d", entity.PlayerID)

	// 第二次保存（UPDATE，因为主键冲突会自动转为更新）
	entity.CurrentStrength = 200
	err = crudRepo.Save(entity)
	if err != nil {
		t.Fatalf("第二次保存（UPSERT）失败: %v", err)
	}
	t.Logf("第二次保存成功（UPSERT）: PlayerID=%d, CurrentStrength=%d", entity.PlayerID, entity.CurrentStrength)

	// 查询验证
	found, err := crudRepo.FindById(int64(1000022), &StrengthEntity{})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if found == nil {
		t.Fatalf("查询结果为空")
	}

	foundEntity := found.(*StrengthEntity)
	if foundEntity.CurrentStrength != 200 {
		t.Errorf("期望 CurrentStrength=200，实际=%d", foundEntity.CurrentStrength)
	}

	t.Logf("✓ 嵌入结构体测试通过")
	t.Logf("✓ UPSERT 测试通过")
	t.Logf("✓ db:\"-\" 忽略字段测试通过")
	t.Logf("✓ 无 db tag 忽略字段测试通过")
}

func TestEmbeddedStructPrimaryKeyDetection(t *testing.T) {
	// 测试主键自动检测
	cm := db233.GetCrudManagerInstance()

	// Clear cache to ensure fresh scan
	cm.ClearPrimaryKeyCache()

	entity := &StrengthEntity{}
	pkColumn := cm.GetPrimaryKeyColumnName(entity)

	if pkColumn != "playerId" {
		t.Errorf("期望主键列名=playerId，实际=%s", pkColumn)
	}

	// 测试主键值获取
	entity.PlayerID = 12345
	pkValue := cm.GetPrimaryKeyValue(entity)

	if pkValue == nil {
		t.Errorf("主键值不应该为 nil")
	} else if pkValueInt, ok := pkValue.(int64); !ok || pkValueInt != 12345 {
		t.Errorf("期望主键值=12345，实际=%v (类型=%T)", pkValue, pkValue)
	}

	t.Logf("✓ 嵌入结构体主键自动检测测试通过")
}
