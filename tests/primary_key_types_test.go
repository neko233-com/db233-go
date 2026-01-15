package tests

import (
	"testing"
	"time"

	db233 "github.com/neko233-com/db233-go/pkg/db233"
)

// =====================================================
// 测试非自增主键（业务主键）
// =====================================================

// RankEntity 排行榜实体（非自增主键）
type RankEntity struct {
	RankId       int    `json:"rankId" db:"rankId" primary_key:"true"` // 非自增主键
	RankKey      string `json:"rankKey" db:"rankKey"`
	RankName     string `json:"rankName" db:"rankName"`
	CreateTimeMs int64  `json:"createTimeMs" db:"createTimeMs"`
	UpdateTimeMs int64  `json:"updateTimeMs" db:"updateTimeMs"`
}

func (e *RankEntity) TableName() string       { return "RankEntity" }
func (e *RankEntity) SerializeBeforeSaveDb()  {}
func (e *RankEntity) DeserializeAfterLoadDb() {}

// UserEntity 用户实体（自增主键）
type UserEntity struct {
	ID       int64  `json:"id" db:"id" primary_key:"true" auto_increment:"true"` // 自增主键
	Username string `json:"username" db:"username"`
	Email    string `json:"email" db:"email"`
}

func (e *UserEntity) TableName() string       { return "UserEntity" }
func (e *UserEntity) SerializeBeforeSaveDb()  {}
func (e *UserEntity) DeserializeAfterLoadDb() {}

// AccountEntity 账户实体（字符串主键）
type AccountEntity struct {
	AccountID string `json:"accountId" db:"accountId" primary_key:"true"` // 非自增主键（字符串）
	Username  string `json:"username" db:"username"`
}

func (e *AccountEntity) TableName() string       { return "AccountEntity" }
func (e *AccountEntity) SerializeBeforeSaveDb()  {}
func (e *AccountEntity) DeserializeAfterLoadDb() {}

// TestPrimaryKeyTypes 测试不同类型的主键
func TestPrimaryKeyTypes(t *testing.T) {
	// 初始化数据库
	db := CreateTestDb(t)
	defer db.DataSource.Close()

	cm := db233.GetCrudManagerInstance()
	repo := db233.NewBaseCrudRepository(db)

	// 清理测试数据
	_, _ = db.DataSource.Exec("DROP TABLE IF EXISTS RankEntity")
	_, _ = db.DataSource.Exec("DROP TABLE IF EXISTS UserEntity")
	_, _ = db.DataSource.Exec("DROP TABLE IF EXISTS AccountEntity")

	// =====================================================
	// 测试 1: 非自增主键（int 类型）- 零值应该报错
	// =====================================================
	t.Run("NonAutoIncrementIntPrimaryKey_ZeroValue_ShouldFail", func(t *testing.T) {
		// 创建表
		cm.AutoMigrateTableSimple(db, &RankEntity{})

		// 创建实体，主键为零值
		entity := &RankEntity{
			RankId:   0, // 零值
			RankKey:  "daily_rank",
			RankName: "每日排行榜",
		}

		// 保存应该失败
		err := repo.Save(entity)
		if err == nil {
			t.Fatal("期望保存失败（主键为零值），但保存成功了")
		}

		// 检查错误消息
		if !contains(err.Error(), "主键字段") && !contains(err.Error(), "不能为零值") {
			t.Errorf("期望错误消息包含'主键字段'和'不能为零值'，实际: %v", err)
		}

		t.Logf("✓ 非自增主键零值测试通过，错误: %v", err)
	})

	// =====================================================
	// 测试 2: 非自增主键（int 类型）- 非零值应该成功
	// =====================================================
	t.Run("NonAutoIncrementIntPrimaryKey_NonZeroValue_ShouldSuccess", func(t *testing.T) {
		// 创建实体，主键为非零值
		entity := &RankEntity{
			RankId:       1001, // 非零值
			RankKey:      "daily_rank",
			RankName:     "每日排行榜",
			CreateTimeMs: time.Now().UnixMilli(),
			UpdateTimeMs: time.Now().UnixMilli(),
		}

		// 保存应该成功
		err := repo.Save(entity)
		if err != nil {
			t.Fatalf("保存失败: %v", err)
		}

		// 验证数据
		var loaded RankEntity
		result, err := repo.FindById(1001, &loaded)
		if err != nil {
			t.Fatalf("查询失败: %v", err)
		}
		if result == nil {
			t.Fatal("期望找到记录，但未找到")
		}

		// 类型断言
		if entity, ok := result.(*RankEntity); ok {
			loaded = *entity
		}

		if loaded.RankId != 1001 {
			t.Errorf("期望 RankId=1001, 实际=%d", loaded.RankId)
		}
		if loaded.RankName != "每日排行榜" {
			t.Errorf("期望 RankName='每日排行榜', 实际=%s", loaded.RankName)
		}

		t.Logf("✓ 非自增主键非零值测试通过: RankId=%d", loaded.RankId)
	})

	// =====================================================
	// 测试 3: 自增主键 - 零值应该被跳过（数据库生成）
	// =====================================================
	t.Run("AutoIncrementPrimaryKey_ZeroValue_ShouldSkip", func(t *testing.T) {
		// 创建表
		cm.AutoMigrateTableSimple(db, &UserEntity{})

		// 创建实体，主键为零值
		entity := &UserEntity{
			ID:       0, // 零值，应该被跳过
			Username: "john",
			Email:    "john@example.com",
		}

		// 保存应该成功（数据库自动生成 ID）
		err := repo.Save(entity)
		if err != nil {
			t.Fatalf("保存失败: %v", err)
		}

		// 验证 ID 已被设置
		if entity.ID <= 0 {
			t.Errorf("期望 ID > 0（数据库生成），实际=%d", entity.ID)
		}

		t.Logf("✓ 自增主键零值测试通过，数据库生成的 ID=%d", entity.ID)
	})

	// =====================================================
	// 测试 4: 字符串主键 - 空字符串应该报错
	// =====================================================
	t.Run("StringPrimaryKey_EmptyValue_ShouldFail", func(t *testing.T) {
		// 创建表
		cm.AutoMigrateTableSimple(db, &AccountEntity{})

		// 创建实体，主键为空字符串
		entity := &AccountEntity{
			AccountID: "", // 空字符串（零值）
			Username:  "john",
		}

		// 保存应该失败
		err := repo.Save(entity)
		if err == nil {
			t.Fatal("期望保存失败（主键为空字符串），但保存成功了")
		}

		t.Logf("✓ 字符串主键空值测试通过，错误: %v", err)
	})

	// =====================================================
	// 测试 5: 字符串主键 - 非空值应该成功
	// =====================================================
	t.Run("StringPrimaryKey_NonEmptyValue_ShouldSuccess", func(t *testing.T) {
		// 创建实体，主键为非空字符串
		entity := &AccountEntity{
			AccountID: "ACC001", // 非空字符串
			Username:  "john",
		}

		// 保存应该成功
		err := repo.Save(entity)
		if err != nil {
			t.Fatalf("保存失败: %v", err)
		}

		// 验证数据
		var loaded AccountEntity
		result, err := repo.FindById("ACC001", &loaded)
		if err != nil {
			t.Fatalf("查询失败: %v", err)
		}
		if result == nil {
			t.Fatal("期望找到记录，但未找到")
		}

		// 类型断言
		if entity, ok := result.(*AccountEntity); ok {
			loaded = *entity
		}

		if loaded.AccountID != "ACC001" {
			t.Errorf("期望 AccountID='ACC001', 实际=%s", loaded.AccountID)
		}

		t.Logf("✓ 字符串主键非空值测试通过: AccountID=%s", loaded.AccountID)
	})

	// =====================================================
	// 测试 6: UPSERT 测试（非自增主键）
	// =====================================================
	t.Run("NonAutoIncrementPrimaryKey_UPSERT", func(t *testing.T) {
		// 第一次保存
		entity := &RankEntity{
			RankId:       2001,
			RankKey:      "weekly_rank",
			RankName:     "每周排行榜",
			CreateTimeMs: time.Now().UnixMilli(),
			UpdateTimeMs: time.Now().UnixMilli(),
		}

		err := repo.Save(entity)
		if err != nil {
			t.Fatalf("第一次保存失败: %v", err)
		}

		// 第二次保存（相同主键，应该执行 UPDATE）
		entity.RankName = "每周排行榜（更新）"
		err = repo.Save(entity)
		if err != nil {
			t.Fatalf("第二次保存失败: %v", err)
		}

		// 验证数据已更新
		var loaded RankEntity
		result, err := repo.FindById(2001, &loaded)
		if err != nil {
			t.Fatalf("查询失败: %v", err)
		}
		if result == nil {
			t.Fatal("期望找到记录，但未找到")
		}

		// 类型断言
		if entity, ok := result.(*RankEntity); ok {
			loaded = *entity
		}

		if loaded.RankName != "每周排行榜（更新）" {
			t.Errorf("期望 RankName='每周排行榜（更新）', 实际=%s", loaded.RankName)
		}

		t.Logf("✓ UPSERT 测试通过: RankName=%s", loaded.RankName)
	})
}

// contains 检查字符串是否包含子串
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsSubstring(s, substr)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
