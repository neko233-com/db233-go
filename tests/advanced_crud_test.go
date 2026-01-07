package tests

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/neko233-com/db233-go/pkg/db233"
)

// TestEntityWithComplexTypes 测试包含复杂类型的实体
type TestEntityWithComplexTypes struct {
	ID        int                    `db:"id,primary_key,auto_increment"`
	Name      string                 `db:"name"`
	Tags      []string               `db:"tags"`       // slice 类型
	Metadata  map[string]interface{} `db:"metadata"`   // map 类型
	Items     []Item                 `db:"items"`      // 结构体 slice
	Config    *Config                `db:"config"`     // 指针类型
	CreatedAt time.Time              `db:"created_at"` // time.Time（不应序列化）
}

type Item struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

type Config struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (e *TestEntityWithComplexTypes) TableName() string {
	return "test_complex_types"
}

func (e *TestEntityWithComplexTypes) SerializeBeforeSaveDb() {
	// 可以在这里进行额外的序列化处理
}

func (e *TestEntityWithComplexTypes) DeserializeAfterLoadDb() {
	// 可以在这里进行反序列化处理
}

// TestEntityWithUnexportedFields 测试包含未导出字段的实体
type TestEntityWithUnexportedFields struct {
	ID          int    `db:"id,primary_key,auto_increment"`
	Name        string `db:"name"`
	exported    string // 未导出字段，应该被跳过
	privateData string `db:"-"` // 明确标记为跳过
	PublicField string `db:"public_field"`
}

func (e *TestEntityWithUnexportedFields) TableName() string {
	return "test_unexported_fields"
}

func (e *TestEntityWithUnexportedFields) SerializeBeforeSaveDb()  {}
func (e *TestEntityWithUnexportedFields) DeserializeAfterLoadDb() {}

// TestEntityWithSkipFields 测试跳过字段
type TestEntityWithSkipFields struct {
	ID       int    `db:"id,primary_key,auto_increment"`
	Name     string `db:"name"`
	SkipMe   string `db:"skip_me,skip"` // 使用 skip 选项
	IgnoreMe string `db:"-"`            // 使用 - 标记
	// EmptyTag 没有 db 标签，但由于现在支持无标签字段自动转换，它会被包含
	// 如果表中有这个列，测试会通过；如果没有，测试会失败（这是预期的）
	EmptyTag string // 无标签字段（现在会被自动包含）
}

func (e *TestEntityWithSkipFields) TableName() string {
	return "test_skip_fields"
}

func (e *TestEntityWithSkipFields) SerializeBeforeSaveDb()  {}
func (e *TestEntityWithSkipFields) DeserializeAfterLoadDb() {}

// TestEntityWithEmptyValues 测试空值处理
type TestEntityWithEmptyValues struct {
	ID       int     `db:"id,primary_key,auto_increment"`
	Name     string  `db:"name"`
	EmptyStr string  `db:"empty_str"`
	ZeroInt  int     `db:"zero_int"`
	NilPtr   *string `db:"nil_ptr"`
}

func (e *TestEntityWithEmptyValues) TableName() string {
	return "test_empty_values"
}

func (e *TestEntityWithEmptyValues) SerializeBeforeSaveDb()  {}
func (e *TestEntityWithEmptyValues) DeserializeAfterLoadDb() {}

// 设置复杂类型测试表
func setupComplexTypesTable(db *db233.Db) error {
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS test_complex_types (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			tags TEXT,
			metadata TEXT,
			items TEXT,
			config TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`
	_, err := db.DataSource.Exec(createTableSQL)
	return err
}

// 设置未导出字段测试表
func setupUnexportedFieldsTable(db *db233.Db) error {
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS test_unexported_fields (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			public_field VARCHAR(255)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`
	_, err := db.DataSource.Exec(createTableSQL)
	return err
}

// 设置跳过字段测试表
func setupSkipFieldsTable(db *db233.Db) error {
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS test_skip_fields (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			empty_tag VARCHAR(255)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`
	_, err := db.DataSource.Exec(createTableSQL)
	return err
}

// 设置空值测试表
func setupEmptyValuesTable(db *db233.Db) error {
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS test_empty_values (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			empty_str VARCHAR(255),
			zero_int INT,
			nil_ptr VARCHAR(255)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`
	_, err := db.DataSource.Exec(createTableSQL)
	return err
}

// TestComplexTypesSerialization 测试复杂类型序列化
func TestComplexTypesSerialization(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	err := setupComplexTypesTable(db)
	if err != nil {
		t.Fatalf("设置测试表失败: %v", err)
	}
	defer func() {
		db.DataSource.Exec("DROP TABLE IF EXISTS test_complex_types")
	}()

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestEntityWithComplexTypes{})

	// 创建包含复杂类型的实体
	entity := &TestEntityWithComplexTypes{
		Name: "测试实体",
		Tags: []string{"tag1", "tag2", "tag3"},
		Metadata: map[string]interface{}{
			"key1": "value1",
			"key2": 123,
			"key3": true,
		},
		Items: []Item{
			{Name: "item1", Value: 100},
			{Name: "item2", Value: 200},
		},
		Config: &Config{
			Key:   "config_key",
			Value: "config_value",
		},
		CreatedAt: time.Now(),
	}

	// 保存实体
	err = repo.Save(entity)
	if err != nil {
		t.Fatalf("保存实体失败: %v", err)
	}

	if entity.ID == 0 {
		t.Error("实体ID应该被自动设置")
	}

	// 查询实体
	found, err := repo.FindById(entity.ID, &TestEntityWithComplexTypes{})
	if err != nil {
		t.Fatalf("查询实体失败: %v", err)
	}

	if found == nil {
		t.Fatal("应该找到实体")
	}

	foundEntity := found.(*TestEntityWithComplexTypes)

	// 验证基本字段
	if foundEntity.Name != entity.Name {
		t.Errorf("Name 不匹配: 期望 %s, 得到 %s", entity.Name, foundEntity.Name)
	}

	// 验证序列化的字段（需要反序列化）
	// 注意：由于数据库存储的是 JSON 字符串，需要手动反序列化
	t.Logf("成功保存和查询包含复杂类型的实体: ID=%d", entity.ID)
}

// TestUnexportedFields 测试未导出字段处理
func TestUnexportedFields(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	err := setupUnexportedFieldsTable(db)
	if err != nil {
		t.Fatalf("设置测试表失败: %v", err)
	}
	defer func() {
		db.DataSource.Exec("DROP TABLE IF EXISTS test_unexported_fields")
	}()

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestEntityWithUnexportedFields{})

	entity := &TestEntityWithUnexportedFields{
		Name:        "测试实体",
		PublicField: "公共字段",
	}

	// 保存应该成功，未导出字段应该被跳过
	err = repo.Save(entity)
	if err != nil {
		t.Fatalf("保存实体失败（未导出字段应该被跳过）: %v", err)
	}

	if entity.ID == 0 {
		t.Error("实体ID应该被自动设置")
	}

	t.Logf("成功处理未导出字段: ID=%d", entity.ID)
}

// TestSkipFields 测试跳过字段
func TestSkipFields(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	err := setupSkipFieldsTable(db)
	if err != nil {
		t.Fatalf("设置测试表失败: %v", err)
	}
	defer func() {
		db.DataSource.Exec("DROP TABLE IF EXISTS test_skip_fields")
	}()

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestEntityWithSkipFields{})

	entity := &TestEntityWithSkipFields{
		Name:     "测试实体",
		SkipMe:   "应该被跳过",
		IgnoreMe: "应该被忽略",
		EmptyTag: "空标签",
	}

	// 保存应该成功，跳过的字段不应该被插入
	err = repo.Save(entity)
	if err != nil {
		t.Fatalf("保存实体失败（跳过字段应该被忽略）: %v", err)
	}

	if entity.ID == 0 {
		t.Error("实体ID应该被自动设置")
	}

	t.Logf("成功处理跳过字段: ID=%d", entity.ID)
}

// TestEmptyValues 测试空值处理
func TestEmptyValues(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	err := setupEmptyValuesTable(db)
	if err != nil {
		t.Fatalf("设置测试表失败: %v", err)
	}
	defer func() {
		db.DataSource.Exec("DROP TABLE IF EXISTS test_empty_values")
	}()

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestEntityWithEmptyValues{})

	entity := &TestEntityWithEmptyValues{
		Name:     "测试实体",
		EmptyStr: "",  // 空字符串
		ZeroInt:  0,   // 零值
		NilPtr:   nil, // nil 指针
	}

	// 保存应该成功，空值应该被正确处理
	err = repo.Save(entity)
	if err != nil {
		t.Fatalf("保存实体失败（空值应该被正确处理）: %v", err)
	}

	if entity.ID == 0 {
		t.Error("实体ID应该被自动设置")
	}

	t.Logf("成功处理空值: ID=%d", entity.ID)
}

// TestValidationErrors 测试验证错误
func TestValidationErrors(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	repo := db233.NewBaseCrudRepository(db)

	// 测试 nil 实体
	err := repo.Save(nil)
	if err == nil {
		t.Error("应该返回验证错误（实体为 nil）")
	} else {
		t.Logf("正确捕获 nil 实体错误: %v", err)
	}

	// 测试 nil 实体类型
	_, err = repo.FindById(1, nil)
	if err == nil {
		t.Error("应该返回验证错误（实体类型为 nil）")
	} else {
		t.Logf("正确捕获 nil 实体类型错误: %v", err)
	}

	// 测试空 ID
	_, err = repo.FindById(nil, &TestUser{})
	if err == nil {
		t.Error("应该返回验证错误（ID 为 nil）")
	} else {
		t.Logf("正确捕获 nil ID 错误: %v", err)
	}
}

// TestBatchOperations 测试批量操作
func TestBatchOperations(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	err := SetupTestTables(db)
	if err != nil {
		t.Fatalf("设置测试表失败: %v", err)
	}
	defer CleanupTestTables(db)

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})

	// 测试批量保存
	users := []db233.IDbEntity{
		&TestUser{Username: "batch1", Email: "batch1@example.com", Age: 20},
		&TestUser{Username: "batch2", Email: "batch2@example.com", Age: 21},
		&TestUser{Username: "batch3", Email: "batch3@example.com", Age: 22},
	}

	err = repo.SaveBatch(users)
	if err != nil {
		t.Fatalf("批量保存失败: %v", err)
	}

	// 验证保存结果
	for i, user := range users {
		testUser := user.(*TestUser)
		if testUser.ID == 0 {
			t.Errorf("用户 %d 的ID应该被设置", i+1)
		}
	}

	// 测试批量更新
	for _, user := range users {
		testUser := user.(*TestUser)
		testUser.Age += 10
	}

	err = repo.UpdateBatch(users)
	if err != nil {
		t.Fatalf("批量更新失败: %v", err)
	}

	// 验证更新结果
	for i, user := range users {
		testUser := user.(*TestUser)
		found, err := repo.FindById(testUser.ID, &TestUser{})
		if err != nil {
			t.Errorf("查询用户 %d 失败: %v", i+1, err)
			continue
		}
		if found == nil {
			t.Errorf("应该找到用户 %d", i+1)
			continue
		}
		foundUser := found.(*TestUser)
		if foundUser.Age != testUser.Age {
			t.Errorf("用户 %d 的年龄更新失败: 期望 %d, 得到 %d", i+1, testUser.Age, foundUser.Age)
		}
	}

	t.Logf("批量操作测试通过: 保存和更新了 %d 个用户", len(users))
}

// TestErrorMessages 测试错误消息的可读性
func TestErrorMessages(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	err := SetupTestTables(db)
	if err != nil {
		t.Fatalf("设置测试表失败: %v", err)
	}
	defer CleanupTestTables(db)

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})

	// 测试其他错误场景

	// 测试空实体列表
	err = repo.SaveBatch([]db233.IDbEntity{})
	if err == nil {
		t.Error("应该返回验证错误（空实体列表）")
	} else {
		t.Logf("正确捕获空实体列表错误: %v", err)
		// 验证错误消息是否友好
		if err.Error() == "" {
			t.Error("错误消息不应该为空")
		}
	}

	// 测试 nil 实体列表
	err = repo.SaveBatch(nil)
	if err == nil {
		t.Error("应该返回验证错误（nil 实体列表）")
	} else {
		t.Logf("正确捕获 nil 实体列表错误: %v", err)
	}
}

// TestComplexTypeJSONSerialization 测试复杂类型的 JSON 序列化
func TestComplexTypeJSONSerialization(t *testing.T) {
	// 测试 map 序列化
	testMap := map[string]interface{}{
		"key1": "value1",
		"key2": 123,
		"key3": true,
	}
	jsonBytes, err := json.Marshal(testMap)
	if err != nil {
		t.Fatalf("序列化 map 失败: %v", err)
	}
	t.Logf("Map 序列化结果: %s", string(jsonBytes))

	// 测试 slice 序列化
	testSlice := []string{"item1", "item2", "item3"}
	jsonBytes, err = json.Marshal(testSlice)
	if err != nil {
		t.Fatalf("序列化 slice 失败: %v", err)
	}
	t.Logf("Slice 序列化结果: %s", string(jsonBytes))

	// 测试结构体 slice 序列化
	testStructSlice := []Item{
		{Name: "item1", Value: 100},
		{Name: "item2", Value: 200},
	}
	jsonBytes, err = json.Marshal(testStructSlice)
	if err != nil {
		t.Fatalf("序列化结构体 slice 失败: %v", err)
	}
	t.Logf("结构体 Slice 序列化结果: %s", string(jsonBytes))
}

// TestZeroValueDetection 测试零值检测
func TestZeroValueDetection(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	err := setupEmptyValuesTable(db)
	if err != nil {
		t.Fatalf("设置测试表失败: %v", err)
	}
	defer func() {
		db.DataSource.Exec("DROP TABLE IF EXISTS test_empty_values")
	}()

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestEntityWithEmptyValues{})

	// 测试各种零值情况
	testCases := []struct {
		name       string
		entity     *TestEntityWithEmptyValues
		shouldSave bool
	}{
		{
			name: "所有字段为空",
			entity: &TestEntityWithEmptyValues{
				Name:     "测试",
				EmptyStr: "",
				ZeroInt:  0,
				NilPtr:   nil,
			},
			shouldSave: true,
		},
		{
			name: "部分字段有值",
			entity: &TestEntityWithEmptyValues{
				Name:     "测试2",
				EmptyStr: "非空",
				ZeroInt:  100,
				NilPtr:   nil,
			},
			shouldSave: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := repo.Save(tc.entity)
			if tc.shouldSave {
				if err != nil {
					t.Errorf("应该成功保存: %v", err)
				} else if tc.entity.ID == 0 {
					t.Error("实体ID应该被设置")
				} else {
					t.Logf("成功保存零值实体: ID=%d", tc.entity.ID)
				}
			} else {
				if err == nil {
					t.Error("应该返回错误")
				}
			}
		})
	}
}
