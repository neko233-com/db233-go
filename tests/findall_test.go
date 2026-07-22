package tests

import (
	"reflect"
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

// TestFindAll_EmptyTable 测试空表 FindAll
func TestFindAll_EmptyTable(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 设置测试环境
	err := SetupTestTables(db)
	if err != nil {
		t.Fatalf("设置测试表失败: %v", err)
	}
	defer CleanupTestTables(db)

	repo := db233.NewBaseCrudRepository(db)

	// 初始化实体元信息
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})

	// 测试空表的 FindAll
	entities, err := repo.FindAll(&TestUser{})
	if err != nil {
		t.Errorf("FindAll 执行失败: %v", err)
		return
	}

	if entities == nil {
		t.Error("FindAll 不应该返回 nil，应该返回空切片")
		return
	}

	if len(entities) != 0 {
		t.Errorf("空表应该返回空切片，但得到: %d 条记录", len(entities))
	}

	t.Logf("空表 FindAll 测试通过: 返回 %d 条记录", len(entities))
}

// TestFindAll_WithData 测试有数据的 FindAll
func TestFindAll_WithData(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 设置日志级别为 DEBUG 以便查看详细信息
	logger := db233.GetLogger()
	previousLevel := logger.GetLevel()
	defer logger.SetLevel(previousLevel)
	logger.SetLevel(db233.DEBUG)

	// 设置测试环境
	err := SetupTestTables(db)
	if err != nil {
		t.Fatalf("设置测试表失败: %v", err)
	}
	defer CleanupTestTables(db)

	repo := db233.NewBaseCrudRepository(db)

	// 初始化实体元信息
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})

	// 先保存几个测试用例
	testUsers := []*TestUser{
		{Username: "findall_user1", Email: "findall1@example.com", Age: 20},
		{Username: "findall_user2", Email: "findall2@example.com", Age: 25},
		{Username: "findall_user3", Email: "findall3@example.com", Age: 30},
	}

	for _, user := range testUsers {
		err := repo.Save(user)
		if err != nil {
			t.Errorf("保存用户失败: %v", err)
			return
		}
		t.Logf("保存用户成功: ID=%d, Username=%s", user.ID, user.Username)
	}

	// 执行 FindAll
	entities, err := repo.FindAll(&TestUser{})
	if err != nil {
		t.Errorf("FindAll 执行失败: %v", err)
		return
	}

	if entities == nil {
		t.Error("FindAll 不应该返回 nil")
		return
	}

	t.Logf("FindAll 返回 %d 条记录", len(entities))

	// 验证返回的记录数
	// 注意：可能包含其他测试留下的数据，所以至少应该有我们刚保存的 3 条
	if len(entities) < 3 {
		t.Errorf("期望至少找到 3 条记录，但只找到 %d 条", len(entities))
	}

	// 验证返回的数据类型和内容
	foundCount := 0
	for i, entity := range entities {
		t.Logf("记录 %d: 类型=%T, 值=%+v", i, entity, entity)

		// 检查类型断言
		if entity == nil {
			t.Errorf("记录 %d 为 nil", i)
			continue
		}

		// 尝试类型断言为 *TestUser
		var user *TestUser
		if v, ok := entity.(*TestUser); ok {
			user = v
		} else {
			// 如果不是指针类型，尝试转换
			v := reflect.ValueOf(entity)
			if v.Kind() != reflect.Ptr {
				// 创建指针
				ptr := reflect.New(v.Type())
				ptr.Elem().Set(v)
				if u, ok := ptr.Interface().(*TestUser); ok {
					user = u
				} else {
					t.Errorf("记录 %d 无法转换为 *TestUser，实际类型 %T", i, entity)
					continue
				}
			} else {
				t.Errorf("记录 %d 是指针但不是 *TestUser，实际类型 %T", i, entity)
				continue
			}
		}

		// 验证是我们保存的用户之一
		if user != nil {
			t.Logf("找到用户: ID=%d, Username=%s, Email=%s, Age=%d",
				user.ID, user.Username, user.Email, user.Age)

			// 检查是否是我们保存的测试用例
			for _, testUser := range testUsers {
				if user.ID == testUser.ID && user.Username == testUser.Username {
					foundCount++
					break
				}
			}
		}
	}

	if foundCount < 3 {
		t.Errorf("期望找到至少 3 条我们保存的测试记录，但只找到了 %d 条", foundCount)
	}

	t.Logf("FindAll 测试完成: 总共 %d 条记录，其中 %d 条是我们保存的测试记录", len(entities), foundCount)
}

// TestFindAll_TypeAssertionIssue 专门测试类型断言问题
func TestFindAll_TypeAssertionIssue(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 设置测试环境
	err := SetupTestTables(db)
	if err != nil {
		t.Fatalf("设置测试表失败: %v", err)
	}
	defer CleanupTestTables(db)

	repo := db233.NewBaseCrudRepository(db)

	// 初始化实体元信息
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})

	// 保存一个测试用例
	user := &TestUser{
		Username: "type_test_user",
		Email:    "type_test@example.com",
		Age:      28,
	}

	err = repo.Save(user)
	if err != nil {
		t.Errorf("保存用户失败: %v", err)
		return
	}

	// 直接调用 ExecuteQuery 来查看返回的结果类型
	sql := "SELECT * FROM test_user WHERE username = ?"
	results := db.ExecuteQuery(sql, [][]any{{"type_test_user"}}, &TestUser{})

	t.Logf("ExecuteQuery 返回 %d 条结果", len(results))

	for i, result := range results {
		t.Logf("结果 %d: 类型=%T, 值=%+v", i, result, result)

		// 检查类型信息
		v := reflect.ValueOf(result)
		t.Logf("结果 %d 的反射信息 Kind=%s, Type=%s", i, v.Kind(), v.Type())

		// 尝试类型断言为 IDbEntity
		if dbEntity, ok := result.(db233.IDbEntity); ok {
			t.Logf("结果 %d 成功断言为 IDbEntity: %+v", i, dbEntity)
		} else {
			t.Logf("结果 %d 无法断言为 IDbEntity，实际类型 %T", i, result)

			// 如果不是指针，尝试转换为指针
			if v.Kind() != reflect.Ptr {
				ptr := reflect.New(v.Type())
				ptr.Elem().Set(v)
				ptrResult := ptr.Interface()
				t.Logf("转换为指针后: 类型=%T", ptrResult)

				if dbEntity, ok := ptrResult.(db233.IDbEntity); ok {
					t.Logf("转换为指针后成功断言为 IDbEntity: %+v", dbEntity)
				} else {
					t.Logf("转换为指针后仍无法断言为 IDbEntity")
				}
			}
		}
	}

	// 执行 FindAll
	entities, err := repo.FindAll(&TestUser{})
	if err != nil {
		t.Errorf("FindAll 执行失败: %v", err)
		return
	}

	t.Logf("FindAll 返回 %d 条记录", len(entities))

	// 如果 ExecuteQuery 有结果但 FindAll 返回空，说明类型断言有问题
	if len(results) > 0 && len(entities) == 0 {
		t.Error("发现问题: ExecuteQuery 返回了结果，但 FindAll 返回空列表，说明类型断言失败")
		t.Logf("这可能是由于 OrmBatch 返回的是值类型，而 IDbEntity 接口的方法是指针接收者")
	}
}
