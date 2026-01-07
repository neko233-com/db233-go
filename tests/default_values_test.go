package tests

import (
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

// TestEntityWithDefaults 测试默认值处理
type TestEntityWithDefaults struct {
	ID          int                    `db:"id,primary_key,auto_increment"`
	Name        string                 `db:"name"`
	EmptyString string                 `db:"empty_string"` // 空字符串字段
	ZeroInt     int                    `db:"zero_int"`     // 零值整数
	ZeroFloat   float64                `db:"zero_float"`   // 零值浮点数
	FalseBool   bool                   `db:"false_bool"`   // false 布尔值
	EmptySlice  []string               `db:"empty_slice"`  // 空切片
	EmptyMap    map[string]interface{} `db:"empty_map"`    // 空 map
	TextField   string                 `db:"text_field"`   // TEXT 类型字段
}

func (e *TestEntityWithDefaults) TableName() string {
	return "test_defaults"
}

func (e *TestEntityWithDefaults) SerializeBeforeSaveDb() {
	// 可以在这里设置默认值
	if e.TextField == "" {
		e.TextField = "{}" // 默认空 JSON 对象
	}
}

func (e *TestEntityWithDefaults) DeserializeAfterLoadDb() {}

// 设置默认值测试表
func setupDefaultsTable(db *db233.Db) error {
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS test_defaults (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			empty_string VARCHAR(255),
			zero_int INT,
			zero_float DOUBLE,
			false_bool TINYINT(1),
			empty_slice TEXT,
			empty_map TEXT,
			text_field TEXT
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`
	_, err := db.DataSource.Exec(createTableSQL)
	return err
}

// TestDefaultValues 测试默认值处理
func TestDefaultValues(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	err := setupDefaultsTable(db)
	if err != nil {
		t.Fatalf("设置测试表失败: %v", err)
	}
	defer func() {
		db.DataSource.Exec("DROP TABLE IF EXISTS test_defaults")
	}()

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestEntityWithDefaults{})

	// 创建包含所有空值的实体
	entity := &TestEntityWithDefaults{
		Name: "测试实体",
		// 所有其他字段都是零值
	}

	// 保存应该成功，空值字段应该被包含在 INSERT 中
	err = repo.Save(entity)
	if err != nil {
		t.Fatalf("保存实体失败（空值字段应该被包含）: %v", err)
	}

	if entity.ID == 0 {
		t.Error("实体ID应该被自动设置")
	}

	// 查询验证
	found, err := repo.FindById(entity.ID, &TestEntityWithDefaults{})
	if err != nil {
		t.Fatalf("查询实体失败: %v", err)
	}

	if found == nil {
		t.Fatal("应该找到实体")
	}

	foundEntity := found.(*TestEntityWithDefaults)

	// 验证默认值
	if foundEntity.EmptyString != "" {
		t.Errorf("EmptyString 应该是空字符串，得到: %s", foundEntity.EmptyString)
	}
	if foundEntity.ZeroInt != 0 {
		t.Errorf("ZeroInt 应该是 0，得到: %d", foundEntity.ZeroInt)
	}
	if foundEntity.ZeroFloat != 0.0 {
		t.Errorf("ZeroFloat 应该是 0.0，得到: %f", foundEntity.ZeroFloat)
	}
	if foundEntity.FalseBool != false {
		t.Errorf("FalseBool 应该是 false，得到: %v", foundEntity.FalseBool)
	}

	t.Logf("成功保存和查询包含默认值的实体: ID=%d", entity.ID)
}

// TestRequiredEntity 测试必填字段实体
type TestRequiredEntity struct {
	PlayerID string `db:"player_id,primary_key"`
	Name     string `db:"name"`
	Data     string `db:"data"`
	Score    int    `db:"score"`
}

func (e *TestRequiredEntity) TableName() string {
	return "test_required_defaults"
}

func (e *TestRequiredEntity) SerializeBeforeSaveDb() {
	// 设置默认值
	if e.Name == "" {
		e.Name = "默认名称"
	}
	if e.Data == "" {
		e.Data = "{}"
	}
	if e.Score == 0 {
		e.Score = 0 // 保持为 0
	}
}

func (e *TestRequiredEntity) DeserializeAfterLoadDb() {}

// TestRequiredFieldsWithDefaults 测试必填字段的默认值
func TestRequiredFieldsWithDefaults(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 创建要求所有字段都有值的表
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS test_required_defaults (
			player_id VARCHAR(255) NOT NULL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			data TEXT NOT NULL,
			score INT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`
	_, err := db.DataSource.Exec(createTableSQL)
	if err != nil {
		t.Fatalf("创建测试表失败: %v", err)
	}
	defer func() {
		db.DataSource.Exec("DROP TABLE IF EXISTS test_required_defaults")
	}()

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestRequiredEntity{})

	// 创建只有 PlayerID 的实体
	entity := &TestRequiredEntity{
		PlayerID: "test_player_001",
		// 其他字段都是零值，应该在 SerializeBeforeSaveDb 中设置默认值
	}

	err = repo.Save(entity)
	if err != nil {
		t.Fatalf("保存实体失败: %v", err)
	}

	t.Logf("成功保存必填字段使用默认值的实体: PlayerID=%s", entity.PlayerID)
}
