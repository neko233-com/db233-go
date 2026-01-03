package tests

import (
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

// TestEntityForAutoCreate 用于测试自动建表的实体
type TestEntityForAutoCreate struct {
	// 主键字段（使用 GetDbUid 指定）
	PlayerID string `json:"playerId" db:"player_id" primary_key:"true"`

	// 有 db 标签的字段
	Name     string `json:"name" db:"name"`
	Age      int    `json:"age" db:"age"`
	Email    string `json:"email" db:"email"`

	// 没有 db 标签的字段（应该自动转换）
	ModulesData     string            `json:"modulesData"` // 应该转换为 modules_data
	Score           int               `json:"score"`       // 应该转换为 score
	ComplexMap      map[string]int    `json:"complexMap"`  // 应该转换为 complex_map，类型为 TEXT
	ComplexSlice    []string          `json:"complexSlice"` // 应该转换为 complex_slice，类型为 TEXT
	ComplexArray    [3]int            `json:"complexArray"` // 应该转换为 complex_array，类型为 TEXT

	// 明确标记为忽略的字段
	IgnoredField string `json:"ignored" db:"-"`

	// 明确标记为 skip 的字段
	SkippedField string `json:"skipped" db:"skip_me,skip"`

	// 使用 db_type 指定类型的字段
	JsonData string `json:"jsonData" db:"json_data" db_type:"TEXT"`

	// 使用 not_null 标记的字段
	RequiredField string `json:"required" db:"required_field,not_null"`

	// 默认允许为 null 的字段
	OptionalField string `json:"optional" db:"optional_field"`
}

func (e *TestEntityForAutoCreate) TableName() string {
	return "test_auto_create"
}

func (e *TestEntityForAutoCreate) GetDbUid() string {
	return "player_id"
}

func (e *TestEntityForAutoCreate) SerializeBeforeSaveDb() {
	// 可以在这里处理复杂类型的序列化
}

func (e *TestEntityForAutoCreate) DeserializeAfterLoadDb() {
	// 可以在这里处理复杂类型的反序列化
}

// TestAutoCreateTableWithIDbEntity 测试基于 IDbEntity 的自动建表
func TestAutoCreateTableWithIDbEntity(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 清理测试表（如果存在）
	db.DataSource.Exec("DROP TABLE IF EXISTS test_auto_create")
	defer func() {
		db.DataSource.Exec("DROP TABLE IF EXISTS test_auto_create")
	}()

	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestEntityForAutoCreate{})

	// 自动创建表
	err := cm.AutoCreateTable(db, &TestEntityForAutoCreate{})
	if err != nil {
		t.Fatalf("自动创建表失败: %v", err)
	}

	// 验证表是否创建成功（通过尝试查询表结构）
	rows, err := db.DataSource.Query(`
		SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_TYPE
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'test_auto_create'
		ORDER BY ORDINAL_POSITION
	`)
	if err != nil {
		t.Fatalf("查询表结构失败: %v", err)
	}
	defer rows.Close()

	columns := make(map[string]map[string]string)
	for rows.Next() {
		var colName, dataType, isNullable, columnType string
		if err := rows.Scan(&colName, &dataType, &isNullable, &columnType); err != nil {
			t.Fatalf("扫描列信息失败: %v", err)
		}
		columns[colName] = map[string]string{
			"dataType":   dataType,
			"isNullable": isNullable,
			"columnType": columnType,
		}
		t.Logf("列: %s, 类型: %s, 可空: %s, 完整类型: %s", colName, dataType, isNullable, columnType)
	}

	// 验证主键字段存在
	if _, exists := columns["player_id"]; !exists {
		t.Error("主键字段 player_id 应该存在")
	}

	// 验证有 db 标签的字段存在
	if _, exists := columns["name"]; !exists {
		t.Error("字段 name 应该存在")
	}
	if _, exists := columns["age"]; !exists {
		t.Error("字段 age 应该存在")
	}
	if _, exists := columns["email"]; !exists {
		t.Error("字段 email 应该存在")
	}

	// 验证没有 db 标签的字段也被创建（自动转换）
	if _, exists := columns["modules_data"]; !exists {
		t.Error("字段 modules_data 应该存在（自动转换）")
	}
	if _, exists := columns["score"]; !exists {
		t.Error("字段 score 应该存在（自动转换）")
	}

	// 验证复杂类型字段被创建为 TEXT
	if colInfo, exists := columns["complex_map"]; exists {
		if colInfo["dataType"] != "text" {
			t.Errorf("complex_map 应该是 TEXT 类型，得到: %s", colInfo["dataType"])
		}
	} else {
		t.Error("字段 complex_map 应该存在（自动转换）")
	}

	if colInfo, exists := columns["complex_slice"]; exists {
		if colInfo["dataType"] != "text" {
			t.Errorf("complex_slice 应该是 TEXT 类型，得到: %s", colInfo["dataType"])
		}
	} else {
		t.Error("字段 complex_slice 应该存在（自动转换）")
	}

	// 验证使用 db_type 的字段
	if colInfo, exists := columns["json_data"]; exists {
		if colInfo["dataType"] != "text" {
			t.Errorf("json_data 应该是 TEXT 类型（通过 db_type 指定），得到: %s", colInfo["dataType"])
		}
	} else {
		t.Error("字段 json_data 应该存在")
	}

	// 验证 not_null 字段
	if colInfo, exists := columns["required_field"]; exists {
		if colInfo["isNullable"] != "NO" {
			t.Errorf("required_field 应该是 NOT NULL，得到: %s", colInfo["isNullable"])
		}
	} else {
		t.Error("字段 required_field 应该存在")
	}

	// 验证默认允许为 null 的字段
	if colInfo, exists := columns["optional_field"]; exists {
		if colInfo["isNullable"] != "YES" {
			t.Errorf("optional_field 应该允许为 NULL，得到: %s", colInfo["isNullable"])
		}
	} else {
		t.Error("字段 optional_field 应该存在")
	}

	// 验证被忽略的字段不存在
	if _, exists := columns["ignored"]; exists {
		t.Error("字段 ignored 不应该存在（被 db:\"-\" 忽略）")
	}
	if _, exists := columns["skip_me"]; exists {
		t.Error("字段 skip_me 不应该存在（被 db:\"skip_me,skip\" 忽略）")
	}

	// 测试保存数据
	repo := db233.NewBaseCrudRepository(db)
	entity := &TestEntityForAutoCreate{
		PlayerID:     "test_player_001",
		Name:         "测试用户",
		Age:          25,
		Email:        "test@example.com",
		ModulesData:  "{}",
		Score:        100,
		RequiredField: "必填字段",
		// OptionalField 留空，应该允许为 null
	}

	err = repo.Save(entity)
	if err != nil {
		t.Fatalf("保存实体失败: %v", err)
	}

	// 验证查询
	found, err := repo.FindById("test_player_001", &TestEntityForAutoCreate{})
	if err != nil {
		t.Fatalf("查询实体失败: %v", err)
	}

	if found == nil {
		t.Fatal("应该找到实体")
	}

	foundEntity := found.(*TestEntityForAutoCreate)
	if foundEntity.Name != "测试用户" {
		t.Errorf("Name 应该是 '测试用户'，得到: %s", foundEntity.Name)
	}
	if foundEntity.OptionalField != "" {
		t.Errorf("OptionalField 应该是空字符串（NULL），得到: %s", foundEntity.OptionalField)
	}

	t.Logf("自动建表测试通过: 成功创建表并执行 CRUD 操作")
}

// TestDefaultNullEntity 测试默认允许为 null 的实体
type TestDefaultNullEntity struct {
	ID          int    `db:"id,primary_key,auto_increment"`
	StringField string `db:"string_field"`        // 默认允许 null
	IntField    int    `db:"int_field"`           // 默认允许 null
	NotNullField string `db:"not_null_field,not_null"` // 明确标记为 not_null
}

func (e *TestDefaultNullEntity) TableName() string {
	return "test_default_null"
}

func (e *TestDefaultNullEntity) GetDbUid() string {
	return "id"
}

func (e *TestDefaultNullEntity) SerializeBeforeSaveDb() {}

func (e *TestDefaultNullEntity) DeserializeAfterLoadDb() {}

// TestAutoCreateTableDefaultNull 测试默认允许为 null 的行为
func TestAutoCreateTableDefaultNull(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 清理测试表
	db.DataSource.Exec("DROP TABLE IF EXISTS test_default_null")
	defer func() {
		db.DataSource.Exec("DROP TABLE IF EXISTS test_default_null")
	}()

	entity := &TestDefaultNullEntity{}

	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(entity)

	err := cm.AutoCreateTable(db, entity)
	if err != nil {
		t.Fatalf("自动创建表失败: %v", err)
	}

	// 验证字段是否允许为 null
	rows, err := db.DataSource.Query(`
		SELECT COLUMN_NAME, IS_NULLABLE
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'test_default_null'
	`)
	if err != nil {
		t.Fatalf("查询表结构失败: %v", err)
	}
	defer rows.Close()

	nullableMap := make(map[string]string)
	for rows.Next() {
		var colName, isNullable string
		if err := rows.Scan(&colName, &isNullable); err != nil {
			t.Fatalf("扫描列信息失败: %v", err)
		}
		nullableMap[colName] = isNullable
	}

	// 验证默认允许为 null 的字段
	if nullableMap["string_field"] != "YES" {
		t.Errorf("string_field 应该允许为 NULL，得到: %s", nullableMap["string_field"])
	}
	if nullableMap["int_field"] != "YES" {
		t.Errorf("int_field 应该允许为 NULL，得到: %s", nullableMap["int_field"])
	}

	// 验证明确标记为 not_null 的字段
	if nullableMap["not_null_field"] != "NO" {
		t.Errorf("not_null_field 应该是 NOT NULL，得到: %s", nullableMap["not_null_field"])
	}

	t.Logf("默认允许为 null 测试通过")
}

