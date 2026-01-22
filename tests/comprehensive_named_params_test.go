package tests

import (
	"testing"
	"time"

	"github.com/neko233-com/db233-go/pkg/db233"
)

// =====================================================
// DB233 命名参数功能单元测试
// =====================================================

// TestNamedParamsBasicUpdateMultiple 测试批量命名参数更新
func TestNamedParamsBasicUpdateMultiple(t *testing.T) {
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

	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})
	repo := db233.NewBaseCrudRepository(db)

	// 插入初始数据
	for i := 1001; i <= 1003; i++ {
		user := TestUser{
			ID:       i,
			Username: "user_" + string(rune(i)),
			Email:    "email@test.com",
			Age:      25,
		}
		if err := repo.Save(&user); err != nil {
			t.Fatalf("保存用户失败: %v", err)
		}
	}

	// 批量更新
	sql := "UPDATE test_user SET username={username}, age={age} WHERE id={id}"
	updates := []map[string]any{
		{"id": 1001, "username": "alice", "age": 26},
		{"id": 1002, "username": "bob", "age": 27},
		{"id": 1003, "username": "charlie", "age": 28},
	}

	affected := db.ExecuteUpdateMultiRowsNamed(sql, updates)
	if affected != 3 {
		t.Errorf("期望更新 3 行，实际 %d 行", affected)
	}

	t.Logf("✅ TestNamedParamsBasicUpdateMultiple PASS")
}

// TestNamedParamsSingleUpdate 测试单行命名参数更新
func TestNamedParamsSingleUpdate(t *testing.T) {
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

	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})
	repo := db233.NewBaseCrudRepository(db)

	user := TestUser{ID: 2001, Username: "test_user", Email: "test@test.com", Age: 30}
	if err := repo.Save(&user); err != nil {
		t.Fatalf("保存用户失败: %v", err)
	}

	sql := "UPDATE test_user SET age={age} WHERE id={id}"
	params := map[string]any{"id": 2001, "age": 31}

	affected, err := db.ExecuteUpdateNamed(sql, params)
	if err != nil {
		t.Errorf("更新失败: %v", err)
	}

	if affected != 1 {
		t.Errorf("期望更新 1 行，实际 %d 行", affected)
	}

	t.Logf("✅ TestNamedParamsSingleUpdate PASS")
}

// TestNamedParamsQuery 测试命名参数查询
func TestNamedParamsQuery(t *testing.T) {
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

	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})
	repo := db233.NewBaseCrudRepository(db)

	// 插入数据
	for i := 3001; i <= 3003; i++ {
		user := TestUser{
			ID:       i,
			Username: "user_" + string(rune(i)),
			Email:    "email@test.com",
			Age:      25 + (i - 3001),
		}
		if err := repo.Save(&user); err != nil {
			t.Fatalf("保存用户失败: %v", err)
		}
	}

	// 查询
	sql := "SELECT * FROM test_user WHERE age > {minAge} ORDER BY id"
	params := map[string]any{"minAge": 25}

	rows := db.QueryNamed(sql, params)
	if len(rows) < 2 {
		t.Errorf("期望查询至少 2 行，实际 %d 行", len(rows))
	}

	t.Logf("✅ TestNamedParamsQuery PASS - 查询 %d 行", len(rows))
}

// TestNamedParamsQueryToInt64 测试返回 int64 的命名参数查询
func TestNamedParamsQueryToInt64(t *testing.T) {
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

	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})
	repo := db233.NewBaseCrudRepository(db)

	// 插入数据
	for i := 4001; i <= 4005; i++ {
		user := TestUser{
			ID:       i,
			Username: "user_" + string(rune(i)),
			Email:    "email@test.com",
			Age:      25,
		}
		if err := repo.Save(&user); err != nil {
			t.Fatalf("保存用户失败: %v", err)
		}
	}

	// 查询计数
	sql := "SELECT COUNT(*) FROM test_user WHERE id >= {minId}"
	params := map[string]any{"minId": 4001}

	count := db.QueryNamedToInt64(sql, params)
	if count != 5 {
		t.Errorf("期望计数 5，实际 %d", count)
	}

	t.Logf("✅ TestNamedParamsQueryToInt64 PASS - 计数 %d", count)
}

// TestNamedParamsQueryToString 测试返回 string 的命名参数查询
func TestNamedParamsQueryToString(t *testing.T) {
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

	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})
	repo := db233.NewBaseCrudRepository(db)

	user := TestUser{ID: 5001, Username: "alice_smith", Email: "alice@test.com", Age: 30}
	if err := repo.Save(&user); err != nil {
		t.Fatalf("保存用户失败: %v", err)
	}

	sql := "SELECT username FROM test_user WHERE id={userId}"
	params := map[string]any{"userId": 5001}

	username := db.QueryNamedToString(sql, params)
	if username != "alice_smith" {
		t.Errorf("期望用户名 'alice_smith'，实际 '%s'", username)
	}

	t.Logf("✅ TestNamedParamsQueryToString PASS - 用户名 %s", username)
}

// TestNamedParamsQueryToInt 测试返回 int 的命名参数查询
func TestNamedParamsQueryToInt(t *testing.T) {
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

	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})
	repo := db233.NewBaseCrudRepository(db)

	user := TestUser{ID: 6001, Username: "bob", Email: "bob@test.com", Age: 35}
	if err := repo.Save(&user); err != nil {
		t.Fatalf("保存用户失败: %v", err)
	}

	sql := "SELECT age FROM test_user WHERE id={userId}"
	params := map[string]any{"userId": 6001}

	age := db.QueryNamedToInt(sql, params)
	if age != 35 {
		t.Errorf("期望年龄 35，实际 %d", age)
	}

	t.Logf("✅ TestNamedParamsQueryToInt PASS - 年龄 %d", age)
}

// TestNamedParamsQueryToFloat64 测试返回 float64 的命名参数查询
func TestNamedParamsQueryToFloat64(t *testing.T) {
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

	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})
	repo := db233.NewBaseCrudRepository(db)

	// 插入数据用于计算平均
	for i := 7001; i <= 7003; i++ {
		user := TestUser{
			ID:       i,
			Username: "user_" + string(rune(i)),
			Email:    "email@test.com",
			Age:      25 + (i - 7001),
		}
		if err := repo.Save(&user); err != nil {
			t.Fatalf("保存用户失败: %v", err)
		}
	}

	sql := "SELECT AVG(age) FROM test_user WHERE id >= {minId}"
	params := map[string]any{"minId": 7001}

	avg := db.QueryNamedToFloat64(sql, params)
	if avg < 25 || avg > 27 {
		t.Errorf("期望平均年龄在 25-27，实际 %f", avg)
	}

	t.Logf("✅ TestNamedParamsQueryToFloat64 PASS - 平均 %f", avg)
}

// TestNamedParamsQueryToInt64Slice 测试返回 []int64 的命名参数查询
func TestNamedParamsQueryToInt64Slice(t *testing.T) {
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

	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})
	repo := db233.NewBaseCrudRepository(db)

	// 插入数据
	for i := 8001; i <= 8005; i++ {
		user := TestUser{
			ID:       i,
			Username: "user_" + string(rune(i)),
			Email:    "email@test.com",
			Age:      25,
		}
		if err := repo.Save(&user); err != nil {
			t.Fatalf("保存用户失败: %v", err)
		}
	}

	sql := "SELECT id FROM test_user WHERE id >= {minId} ORDER BY id"
	params := map[string]any{"minId": 8001}

	ids := db.QueryNamedToInt64Slice(sql, params)
	if len(ids) != 5 {
		t.Errorf("期望 5 个 ID，实际 %d 个", len(ids))
	}

	t.Logf("✅ TestNamedParamsQueryToInt64Slice PASS - 查询 %d 个 ID", len(ids))
}

// TestNamedParamsQueryToStringSlice 测试返回 []string 的命名参数查询
func TestNamedParamsQueryToStringSlice(t *testing.T) {
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

	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})
	repo := db233.NewBaseCrudRepository(db)

	// 插入数据
	for i := 9001; i <= 9003; i++ {
		user := TestUser{
			ID:       i,
			Username: "user_" + string(rune(i)),
			Email:    "email@test.com",
			Age:      25,
		}
		if err := repo.Save(&user); err != nil {
			t.Fatalf("保存用户失败: %v", err)
		}
	}

	sql := "SELECT username FROM test_user WHERE id >= {minId} ORDER BY id"
	params := map[string]any{"minId": 9001}

	usernames := db.QueryNamedToStringSlice(sql, params)
	if len(usernames) != 3 {
		t.Errorf("期望 3 个用户名，实际 %d 个", len(usernames))
	}

	t.Logf("✅ TestNamedParamsQueryToStringSlice PASS - 查询 %d 个用户名", len(usernames))
}

// TestNamedParamsPerformance 性能测试
func TestNamedParamsPerformance(t *testing.T) {
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

	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})
	repo := db233.NewBaseCrudRepository(db)

	// 插入初始数据
	for i := 10001; i <= 10010; i++ {
		user := TestUser{
			ID:       i,
			Username: "user_" + string(rune(i)),
			Email:    "email@test.com",
			Age:      25,
		}
		if err := repo.Save(&user); err != nil {
			t.Fatalf("保存用户失败: %v", err)
		}
	}

	// 性能测试：10 行批量更新
	startTime := time.Now()

	sql := "UPDATE test_user SET age={age} WHERE id={id}"
	var updates []map[string]any
	for i := 10001; i <= 10010; i++ {
		updates = append(updates, map[string]any{
			"id":  i,
			"age": 50 + (i - 10001),
		})
	}

	affected := db.ExecuteUpdateMultiRowsNamed(sql, updates)
	elapsed := time.Since(startTime)

	if affected != 10 {
		t.Errorf("期望更新 10 行，实际 %d 行", affected)
	}

	t.Logf("✅ TestNamedParamsPerformance PASS - 10 行批量更新耗时 %v", elapsed)
}
