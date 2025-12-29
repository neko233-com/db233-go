package tests

import (
	"reflect"
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

func TestBaseCrudRepository_Save(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 设置测试表
	err := SetupTestTables(db)
	if err != nil {
		t.Fatalf("设置测试表失败: %v", err)
	}
	defer CleanupTestTables(db) // 测试结束后清理

	repo := db233.NewBaseCrudRepository(db)

	// 初始化实体
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})

	// 创建测试用户
	user := &TestUser{
		Username: "testuser",
		Email:    "test@example.com",
		Age:      25,
	}

	// 保存用户
	err = repo.Save(user)
	if err != nil {
		t.Errorf("保存用户失败: %v", err)
		return
	}

	if user.ID == 0 {
		t.Error("用户ID应该被自动设置")
	}

	t.Logf("成功保存用户: ID=%d", user.ID)
}

func TestBaseCrudRepository_FindById(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 设置测试表
	err := SetupTestTables(db)
	if err != nil {
		t.Fatalf("设置测试表失败: %v", err)
	}
	defer CleanupTestTables(db)

	repo := db233.NewBaseCrudRepository(db)

	// 初始化实体
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})

	// 先保存一个用户
	user := &TestUser{
		Username: "findtest",
		Email:    "find@example.com",
		Age:      30,
	}

	err = repo.Save(user)
	if err != nil {
		t.Errorf("保存用户失败: %v", err)
		return
	}

	// 根据ID查找
	found, err := repo.FindById(user.ID, &TestUser{})
	if err != nil {
		t.Errorf("查找用户失败: %v", err)
		return
	}

	if found == nil {
		t.Error("应该找到用户")
		return
	}

	// 类型断言
	var foundUser *TestUser
	switch v := found.(type) {
	case TestUser:
		foundUser = &v
	case *TestUser:
		foundUser = v
	default:
		t.Errorf("意外的返回类型: %T", found)
		return
	}

	if foundUser.Username != "findtest" {
		t.Errorf("用户名不匹配: 期望 %s, 得到 %s", "findtest", foundUser.Username)
	}

	t.Logf("成功查找用户: %+v", foundUser)
}

func TestBaseCrudRepository_Update(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 设置测试表
	err := SetupTestTables(db)
	if err != nil {
		t.Fatalf("设置测试表失败: %v", err)
	}
	defer CleanupTestTables(db)

	repo := db233.NewBaseCrudRepository(db)

	// 初始化实体
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})

	// 先保存一个用户
	user := &TestUser{
		Username: "updatetest",
		Email:    "update@example.com",
		Age:      20,
	}

	err = repo.Save(user)
	if err != nil {
		t.Errorf("保存用户失败: %v", err)
		return
	}

	// 更新用户信息
	user.Age = 29
	user.Email = "updated@example.com"

	t.Logf("更新前用户: %+v", user)

	err = repo.Update(user)
	if err != nil {
		t.Errorf("更新用户失败: %v", err)
		return
	}

	t.Logf("更新后用户对象: %+v", user)

	// 验证更新
	found, err := repo.FindById(user.ID, &TestUser{})
	if err != nil {
		t.Errorf("查找更新后的用户失败: %v", err)
		return
	}

	if found == nil {
		t.Errorf("查找更新后的用户失败")
		return
	}

	// 类型断言
	var foundUser *TestUser
	switch v := found.(type) {
	case TestUser:
		foundUser = &v
	case *TestUser:
		foundUser = v
	default:
		t.Errorf("意外的返回类型: %T", found)
		return
	}

	if foundUser.Age != 29 || foundUser.Email != "updated@example.com" {
		t.Errorf("更新未生效: %+v", foundUser)
	}

	t.Logf("成功更新用户: %+v", foundUser)
}

func TestBaseCrudRepository_DeleteById(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 设置测试表
	err := SetupTestTables(db)
	if err != nil {
		t.Fatalf("设置测试表失败: %v", err)
	}
	defer CleanupTestTables(db)

	repo := db233.NewBaseCrudRepository(db)

	// 初始化实体
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})

	// 先保存一个用户
	user := &TestUser{
		Username: "deletetest",
		Email:    "delete@example.com",
		Age:      35,
	}

	err = repo.Save(user)
	if err != nil {
		t.Errorf("保存用户失败: %v", err)
		return
	}

	userID := user.ID

	// 删除用户
	err = repo.DeleteById(userID, &TestUser{})
	if err != nil {
		t.Errorf("删除用户失败: %v", err)
		return
	}

	// 验证删除
	found, err := repo.FindById(userID, &TestUser{})
	if err != nil {
		t.Errorf("查找删除后的用户失败: %v", err)
		return
	}

	if found != nil {
		t.Error("用户应该已被删除")
	}

	t.Logf("成功删除用户 ID: %d", userID)
}

func TestBaseCrudRepository_Count(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 设置测试表
	err := SetupTestTables(db)
	if err != nil {
		t.Fatalf("设置测试表失败: %v", err)
	}
	defer CleanupTestTables(db)

	repo := db233.NewBaseCrudRepository(db)

	// 初始化实体
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})

	// 获取初始计数
	initialCount, err := repo.Count(&TestUser{})
	if err != nil {
		t.Errorf("获取初始计数失败: %v", err)
		return
	}

	// 保存几个用户
	for i := 0; i < 3; i++ {
		user := &TestUser{
			Username: "counttest" + string(rune(i+'0')),
			Email:    "count" + string(rune(i+'0')) + "@example.com",
			Age:      20 + i,
		}
		err := repo.Save(user)
		if err != nil {
			t.Errorf("保存用户失败: %v", err)
			return
		}
	}

	// 获取新计数
	newCount, err := repo.Count(&TestUser{})
	if err != nil {
		t.Errorf("获取新计数失败: %v", err)
		return
	}

	expected := initialCount + 3
	if newCount != expected {
		t.Errorf("计数不正确: 期望 %d, 得到 %d", expected, newCount)
	}

	t.Logf("计数测试通过: 初始 %d, 新 %d", initialCount, newCount)
}

func TestCrudManager_AutoInitEntity(t *testing.T) {
	cm := db233.GetCrudManagerInstance()

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
	cm := db233.GetCrudManagerInstance()

	type TestEntity struct {
		ID int `db:"id,primary_key"`
	}

	entity := &TestEntity{}

	// 初始状态不包含
	if cm.IsContainsEntity(entity) {
		t.Error("Entity should not be contained initially")
	}

	// 初始化后应该包含
	cm.AutoInitEntity(entity)
	if !cm.IsContainsEntity(entity) {
		t.Error("Entity should be contained after initialization")
	}
}

func TestCrudManager_GetTableName(t *testing.T) {
	cm := db233.GetCrudManagerInstance()

	type TestUser struct {
		ID int
	}

	t2 := reflect.TypeOf(&TestUser{}).Elem()
	tableName := cm.GetTableName(t2)

	expected := "test_user"
	if tableName != expected {
		t.Errorf("Expected table name %s, got %s", expected, tableName)
	}
}

func TestCrudManager_GetColumnName(t *testing.T) {
	cm := db233.GetCrudManagerInstance()

	field := reflect.StructField{
		Name: "UserName",
		Tag:  `db:"user_name"`,
	}

	colName := cm.GetColumnName(field)
	expected := "user_name"
	if colName != expected {
		t.Errorf("Expected column name %s, got %s", expected, colName)
	}
}

func TestCrudManager_IsPrimaryKey(t *testing.T) {
	cm := db233.GetCrudManagerInstance()

	// 测试有 primary_key tag 的字段
	field1 := reflect.StructField{
		Name: "ID",
		Tag:  `db:"id,primary_key"`,
	}
	if !cm.IsPrimaryKey(field1) {
		t.Error("Field with primary_key tag should be primary key")
	}

	// 测试名为 ID 的字段
	field2 := reflect.StructField{
		Name: "ID",
	}
	if !cm.IsPrimaryKey(field2) {
		t.Error("Field named ID should be primary key")
	}

	// 测试普通字段
	field3 := reflect.StructField{
		Name: "Name",
	}
	if cm.IsPrimaryKey(field3) {
		t.Error("Regular field should not be primary key")
	}
}
