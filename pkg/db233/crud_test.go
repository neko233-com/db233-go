package db233

import (
	"database/sql"
	"reflect"
	"testing"
)

// createMockDataSource 创建模拟数据源
func createMockDataSource() *sql.DB {
	// 使用内存 SQLite 进行测试（如果可用）
	// 这里简化处理，返回 nil，实际测试中会跳过数据库操作
	return nil
}

func TestBaseCrudRepository_Save(t *testing.T) {
	// 跳过数据库操作测试，因为我们没有真实的数据库连接
	t.Skip("Skipping database operation test - no real database connection")

	// 如果要测试逻辑，可以创建 mock
	// dataSource := createMockDataSource()
	// db := NewDb(dataSource, 1, nil)
	// repo := NewBaseCrudRepository(db)
	// ... 测试逻辑
}

func TestBaseCrudRepository_FindById(t *testing.T) {
	t.Skip("Skipping database operation test - no real database connection")
}

func TestBaseCrudRepository_Update(t *testing.T) {
	t.Skip("Skipping database operation test - no real database connection")
}

func TestBaseCrudRepository_DeleteById(t *testing.T) {
	t.Skip("Skipping database operation test - no real database connection")
}

func TestBaseCrudRepository_Count(t *testing.T) {
	t.Skip("Skipping database operation test - no real database connection")
}

func TestCrudManager_AutoInitEntity(t *testing.T) {
	cm := GetCrudManagerInstance()

	type TestEntity struct {
		ID   int    `db:"id,primary_key"`
		Name string `db:"name"`
		Age  int    `db:"age"`
	}

	cm.AutoInitEntity(&TestEntity{})

	// 检查元数据是否正确初始化
	tableToPkMap := cm.GetTableToPkColListMap()
	if len(tableToPkMap) == 0 {
		t.Error("Primary key metadata not initialized")
	}

	t.Logf("Primary key map: %v", tableToPkMap)
}

func TestCrudManager_IsContainsEntity(t *testing.T) {
	cm := GetCrudManagerInstance()

	type TestEntity struct {
		ID int `db:"id,primary_key"`
	}

	entity := &TestEntity{}

	// 初始状态不包含
	if cm.isContainsEntity(entity) {
		t.Error("Entity should not be contained initially")
	}

	// 初始化后应该包含
	cm.AutoInitEntity(entity)
	if !cm.isContainsEntity(entity) {
		t.Error("Entity should be contained after initialization")
	}
}

func TestCrudManager_GetTableName(t *testing.T) {
	cm := GetCrudManagerInstance()

	type TestUser struct {
		ID int
	}

	t2 := reflect.TypeOf(&TestUser{}).Elem()
	tableName := cm.getTableName(t2)

	expected := "test_user"
	if tableName != expected {
		t.Errorf("Expected table name %s, got %s", expected, tableName)
	}
}

func TestCrudManager_GetColumnName(t *testing.T) {
	cm := GetCrudManagerInstance()

	field := reflect.StructField{
		Name: "UserName",
		Tag:  `db:"user_name"`,
	}

	colName := cm.getColumnName(field)
	expected := "user_name"
	if colName != expected {
		t.Errorf("Expected column name %s, got %s", expected, colName)
	}
}

func TestCrudManager_IsPrimaryKey(t *testing.T) {
	cm := GetCrudManagerInstance()

	// 测试有 primary_key tag 的字段
	field1 := reflect.StructField{
		Name: "ID",
		Tag:  `db:"id,primary_key"`,
	}
	if !cm.isPrimaryKey(field1) {
		t.Error("Field with primary_key tag should be primary key")
	}

	// 测试名为 ID 的字段
	field2 := reflect.StructField{
		Name: "ID",
	}
	if !cm.isPrimaryKey(field2) {
		t.Error("Field named ID should be primary key")
	}

	// 测试普通字段
	field3 := reflect.StructField{
		Name: "Name",
	}
	if cm.isPrimaryKey(field3) {
		t.Error("Regular field should not be primary key")
	}
}
