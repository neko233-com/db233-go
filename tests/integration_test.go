package tests

import (
	"testing"

	"github.com/SolarisNeko/db233-go/pkg/db233"
)

// TestCrudOperationsIntegration 集成测试：完整的 CRUD 操作
func TestCrudOperationsIntegration(t *testing.T) {
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

	// 测试保存
	user := &TestUser{
		Username: "integration_test",
		Email:    "integration@example.com",
		Age:      28,
	}

	err = repo.Save(user)
	if err != nil {
		t.Errorf("保存用户失败: %v", err)
		return
	}

	if user.ID == 0 {
		t.Error("用户ID应该被自动设置")
	}

	// 测试查找
	found, err := repo.FindById(user.ID, &TestUser{})
	if err != nil {
		t.Errorf("查找用户失败: %v", err)
		return
	}

	if found == nil {
		t.Error("应该找到用户")
		return
	}

	foundUser, ok := found.(*TestUser)
	if !ok {
		t.Errorf("类型转换失败: %T", found)
		return
	}
	if foundUser.Username != "integration_test" {
		t.Errorf("用户名不匹配: 期望 %s, 得到 %s", "integration_test", foundUser.Username)
	}

	// 测试更新
	foundUser.Age = 29
	err = repo.Update(foundUser)
	if err != nil {
		t.Errorf("更新用户失败: %v", err)
		return
	}

	// 验证更新
	updated, err := repo.FindById(user.ID, &TestUser{})
	if err != nil {
		t.Errorf("查找更新后的用户失败: %v", err)
		return
	}

	updatedUser, ok := updated.(*TestUser)
	if !ok {
		t.Errorf("类型转换失败: %T", updated)
		return
	}
	if updatedUser.Age != 29 {
		t.Errorf("年龄未更新: 期望 %d, 得到 %d", 29, updatedUser.Age)
	}

	// 测试计数
	count, err := repo.Count(&TestUser{})
	if err != nil {
		t.Errorf("获取计数失败: %v", err)
		return
	}

	if count == 0 {
		t.Error("计数应该大于0")
	}

	// 测试删除
	err = repo.DeleteById(user.ID, &TestUser{})
	if err != nil {
		t.Errorf("删除用户失败: %v", err)
		return
	}

	// 验证删除
	deleted, err := repo.FindById(user.ID, &TestUser{})
	if err != nil {
		t.Errorf("查找删除后的用户失败: %v", err)
		return
	}

	if deleted != nil {
		t.Error("用户应该已被删除")
	}

	t.Logf("集成测试通过: 成功执行完整的 CRUD 操作")
}
