package tests

import (
	"fmt"
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

// TestExecuteQueryToInt64ByStatement 演示新的便利方法：ExecuteQueryToInt64ByStatement
// 简化了原本需要大量类型转换的代码
func TestExecuteQueryToInt64ByStatement(t *testing.T) {
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

	// 初始化实体和创建 repository
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})
	repo := db233.NewBaseCrudRepository(db)

	// 插入测试数据
	user := &TestUser{
		Username: "convenience_test_user",
		Email:    "convenience@test.com",
		Age:      25,
	}
	err = repo.Save(user)
	if err != nil {
		t.Fatalf("保存用户失败: %v", err)
	}

	// === 原本的写法（复杂、容易出错）===
	// totalSQL := "SELECT COUNT(*) as cnt FROM test_user"
	// totalStmt := db233.NewQueryStatement(totalSQL, map[string]any{})
	// totalRows := db.ExecuteQueryByStatement(totalStmt)
	// var totalLogins int64 = 0
	// if len(totalRows) > 0 {
	//     if rowMap, ok := totalRows[0].(map[string]interface{}); ok {
	//         if cnt, ok2 := rowMap["cnt"].(int64); ok2 {
	//             totalLogins = cnt
	//         } else if cntFloat, ok2 := rowMap["cnt"].(float64); ok2 {
	//             totalLogins = int64(cntFloat)
	//         }
	//     }
	// }

	// === 新方法（简洁、直观）===
	countSQL := "SELECT COUNT(*) as cnt FROM test_user WHERE age > 20"
	count := db.ExecuteQueryToInt64(countSQL)
	if count <= 0 {
		t.Fatalf("预期查询结果大于 0，实际得到: %d", count)
	}
	t.Logf("✓ ExecuteQueryToInt64 测试通过，查询结果: %d", count)

	// 使用 SqlStatement 的便利方法
	stmt := db233.NewQueryStatement(countSQL, nil)
	countByStmt := db.ExecuteQueryToInt64ByStatement(stmt)
	if countByStmt != count {
		t.Fatalf("两种方法结果不一致: %d vs %d", count, countByStmt)
	}
	t.Logf("✓ ExecuteQueryToInt64ByStatement 测试通过，查询结果: %d", countByStmt)
}

// TestExecuteQueryToStringByStatement 演示返回字符串的便利方法
func TestExecuteQueryToStringByStatement(t *testing.T) {
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

	// 初始化实体和创建 repository
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})
	repo := db233.NewBaseCrudRepository(db)

	// 插入测试数据
	user := &TestUser{
		ID:       101,
		Username: "string_test_user",
		Email:    "string@test.com",
		Age:      30,
	}
	err = repo.Save(user)
	if err != nil {
		t.Fatalf("保存用户失败: %v", err)
	}

	// 查询用户名
	usernameSQL := "SELECT username FROM test_user WHERE id = 101"
	username := db.ExecuteQueryToString(usernameSQL)
	if username != "string_test_user" {
		t.Fatalf("预期用户名 'string_test_user'，实际得到: %s", username)
	}
	t.Logf("✓ ExecuteQueryToString 测试通过，查询结果: %s", username)

	// 使用 SqlStatement
	stmt := db233.NewQueryStatement(usernameSQL, nil)
	usernameByStmt := db.ExecuteQueryToStringByStatement(stmt)
	if usernameByStmt != username {
		t.Fatalf("两种方法结果不一致: %s vs %s", username, usernameByStmt)
	}
	t.Logf("✓ ExecuteQueryToStringByStatement 测试通过，查询结果: %s", usernameByStmt)
}

// TestExecuteQueryToIntSlice 演示返回整数切片的便利方法
func TestExecuteQueryToIntSlice(t *testing.T) {
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

	// 初始化实体和创建 repository
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})
	repo := db233.NewBaseCrudRepository(db)

	// 插入测试数据
	for i := 102; i <= 105; i++ {
		user := &TestUser{
			ID:       i,
			Username: fmt.Sprintf("user_%d", i),
			Email:    fmt.Sprintf("user%d@test.com", i),
			Age:      20 + i%10,
		}
		if err := repo.Save(user); err != nil {
			t.Fatalf("保存用户失败: %v", err)
		}
	}

	// 查询所有用户的年龄
	ageSQL := "SELECT age FROM test_user WHERE id BETWEEN 102 AND 105 ORDER BY id"
	ages := db.ExecuteQueryToIntSlice(ageSQL)
	if len(ages) != 4 {
		t.Fatalf("预期查询 4 条记录，实际得到: %d", len(ages))
	}
	t.Logf("✓ ExecuteQueryToIntSlice 测试通过，查询结果: %v", ages)
}

// TestExecuteQueryToInt64Slice 演示返回 int64 切片的便利方法
func TestExecuteQueryToInt64Slice(t *testing.T) {
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

	// 查询所有用户的 ID
	idSQL := "SELECT id FROM test_user WHERE id >= 100 ORDER BY id"
	ids := db.ExecuteQueryToInt64Slice(idSQL)
	if len(ids) < 0 {
		t.Fatalf("预期查询至少 0 条记录，实际得到: %d", len(ids))
	}
	t.Logf("✓ ExecuteQueryToInt64Slice 测试通过，查询结果: %v", ids)
}

// TestExecuteQueryToStringSlice 演示返回字符串切片的便利方法
func TestExecuteQueryToStringSlice(t *testing.T) {
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

	// 查询所有用户的用户名
	usernameSQL := "SELECT username FROM test_user WHERE id >= 100 ORDER BY id"
	usernames := db.ExecuteQueryToStringSlice(usernameSQL)
	if len(usernames) < 0 {
		t.Fatalf("预期查询至少 0 条记录，实际得到: %d", len(usernames))
	}
	t.Logf("✓ ExecuteQueryToStringSlice 测试通过，查询结果: %v", usernames)
}

// TestExecuteQueryToFloat64Slice 演示返回浮点数切片的便利方法
func TestExecuteQueryToFloat64Slice(t *testing.T) {
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

	// 初始化实体和创建 repository
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})
	repo := db233.NewBaseCrudRepository(db)

	// 插入测试数据
	for i := 102; i <= 105; i++ {
		user := &TestUser{
			ID:       i,
			Username: fmt.Sprintf("user_%d", i),
			Email:    fmt.Sprintf("user%d@test.com", i),
			Age:      20 + i%10,
		}
		if err := repo.Save(user); err != nil {
			t.Fatalf("保存用户失败: %v", err)
		}
	}

	// 查询所有用户的年龄作为浮点数
	ageSQL := "SELECT age FROM test_user WHERE id BETWEEN 102 AND 105 ORDER BY id"
	ages := db.ExecuteQueryToFloat64Slice(ageSQL)
	if len(ages) != 4 {
		t.Fatalf("预期查询 4 条记录，实际得到: %d", len(ages))
	}
	t.Logf("✓ ExecuteQueryToFloat64Slice 测试通过，查询结果: %v", ages)
}
