package tests

import (
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

// TestOLAPQuery_CountInt64 测试 COUNT(*) 返回 int64
func TestOLAPQuery_CountInt64(t *testing.T) {
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

	// 先插入一些测试数据
	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUser{})

	testUsers := []*TestUser{
		{Username: "olap_user1", Email: "olap1@example.com", Age: 20},
		{Username: "olap_user2", Email: "olap2@example.com", Age: 25},
		{Username: "olap_user3", Email: "olap3@example.com", Age: 30},
	}

	for _, user := range testUsers {
		err := repo.Save(user)
		if err != nil {
			t.Fatalf("保存用户失败: %v", err)
		}
	}

	// 测试 COUNT(*) 返回 int64
	var returnType int64
	results := db.ExecuteQuery("SELECT COUNT(*) as cnt FROM test_user", [][]any{}, returnType)

	if len(results) == 0 {
		t.Fatal("COUNT 查询应该返回至少一个结果")
	}

	count, ok := results[0].(int64)
	if !ok {
		t.Fatalf("期望返回 int64，实际类型: %T, 值: %v", results[0], results[0])
	}

	if count < 3 {
		t.Errorf("期望至少 3 条记录，实际: %d", count)
	}

	t.Logf("COUNT 查询成功: 返回 int64 值 %d", count)
}

// TestOLAPQuery_CountInt 测试 COUNT(*) 返回 int
func TestOLAPQuery_CountInt(t *testing.T) {
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

	// 测试 COUNT(*) 返回 int
	var returnType int
	results := db.ExecuteQuery("SELECT COUNT(*) as total_count FROM test_user", [][]any{}, returnType)

	if len(results) == 0 {
		t.Fatal("COUNT 查询应该返回至少一个结果")
	}

	count, ok := results[0].(int)
	if !ok {
		t.Fatalf("期望返回 int，实际类型: %T, 值: %v", results[0], results[0])
	}

	t.Logf("COUNT 查询成功: 返回 int 值 %d", count)
}

// TestOLAPQuery_SumFloat64 测试 SUM() 返回 float64
func TestOLAPQuery_SumFloat64(t *testing.T) {
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

	// 插入测试数据
	testUsers := []*TestUser{
		{Username: "sum_user1", Email: "sum1@example.com", Age: 20},
		{Username: "sum_user2", Email: "sum2@example.com", Age: 30},
		{Username: "sum_user3", Email: "sum3@example.com", Age: 40},
	}

	for _, user := range testUsers {
		err := repo.Save(user)
		if err != nil {
			t.Fatalf("保存用户失败: %v", err)
		}
	}

	// 测试 SUM(age) 返回 float64
	var returnType float64
	results := db.ExecuteQuery("SELECT SUM(age) as total_age FROM test_user", [][]any{}, returnType)

	if len(results) == 0 {
		t.Fatal("SUM 查询应该返回至少一个结果")
	}

	sum, ok := results[0].(float64)
	if !ok {
		t.Fatalf("期望返回 float64，实际类型: %T, 值: %v", results[0], results[0])
	}

	expectedSum := float64(20 + 30 + 40)
	if sum < expectedSum {
		t.Errorf("期望 SUM >= %f，实际: %f", expectedSum, sum)
	}

	t.Logf("SUM 查询成功: 返回 float64 值 %f", sum)
}

// TestOLAPQuery_AvgFloat32 测试 AVG() 返回 float32
func TestOLAPQuery_AvgFloat32(t *testing.T) {
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

	// 插入测试数据
	testUsers := []*TestUser{
		{Username: "avg_user1", Email: "avg1@example.com", Age: 20},
		{Username: "avg_user2", Email: "avg2@example.com", Age: 30},
		{Username: "avg_user3", Email: "avg3@example.com", Age: 40},
	}

	for _, user := range testUsers {
		err := repo.Save(user)
		if err != nil {
			t.Fatalf("保存用户失败: %v", err)
		}
	}

	// 测试 AVG(age) 返回 float32
	var returnType float32
	results := db.ExecuteQuery("SELECT AVG(age) as avg_age FROM test_user", [][]any{}, returnType)

	if len(results) == 0 {
		t.Fatal("AVG 查询应该返回至少一个结果")
	}

	avg, ok := results[0].(float32)
	if !ok {
		t.Fatalf("期望返回 float32，实际类型: %T, 值: %v", results[0], results[0])
	}

	expectedAvg := float32(30.0)
	if avg < expectedAvg-1 || avg > expectedAvg+1 {
		t.Errorf("期望 AVG 约等于 %f，实际: %f", expectedAvg, avg)
	}

	t.Logf("AVG 查询成功: 返回 float32 值 %f", avg)
}

// TestOLAPQuery_MaxInt 测试 MAX() 返回 int
func TestOLAPQuery_MaxInt(t *testing.T) {
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

	// 插入测试数据
	testUsers := []*TestUser{
		{Username: "max_user1", Email: "max1@example.com", Age: 20},
		{Username: "max_user2", Email: "max2@example.com", Age: 50},
		{Username: "max_user3", Email: "max3@example.com", Age: 30},
	}

	for _, user := range testUsers {
		err := repo.Save(user)
		if err != nil {
			t.Fatalf("保存用户失败: %v", err)
		}
	}

	// 测试 MAX(age) 返回 int
	var returnType int
	results := db.ExecuteQuery("SELECT MAX(age) as max_age FROM test_user", [][]any{}, returnType)

	if len(results) == 0 {
		t.Fatal("MAX 查询应该返回至少一个结果")
	}

	maxAge, ok := results[0].(int)
	if !ok {
		t.Fatalf("期望返回 int，实际类型: %T, 值: %v", results[0], results[0])
	}

	if maxAge != 50 {
		t.Errorf("期望 MAX(age) = 50，实际: %d", maxAge)
	}

	t.Logf("MAX 查询成功: 返回 int 值 %d", maxAge)
}

// TestOLAPQuery_MinInt64 测试 MIN() 返回 int64
func TestOLAPQuery_MinInt64(t *testing.T) {
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

	// 插入测试数据
	testUsers := []*TestUser{
		{Username: "min_user1", Email: "min1@example.com", Age: 20},
		{Username: "min_user2", Email: "min2@example.com", Age: 50},
		{Username: "min_user3", Email: "min3@example.com", Age: 30},
	}

	for _, user := range testUsers {
		err := repo.Save(user)
		if err != nil {
			t.Fatalf("保存用户失败: %v", err)
		}
	}

	// 测试 MIN(age) 返回 int64
	var returnType int64
	results := db.ExecuteQuery("SELECT MIN(age) as min_age FROM test_user", [][]any{}, returnType)

	if len(results) == 0 {
		t.Fatal("MIN 查询应该返回至少一个结果")
	}

	minAge, ok := results[0].(int64)
	if !ok {
		t.Fatalf("期望返回 int64，实际类型: %T, 值: %v", results[0], results[0])
	}

	if minAge != 20 {
		t.Errorf("期望 MIN(age) = 20，实际: %d", minAge)
	}

	t.Logf("MIN 查询成功: 返回 int64 值 %d", minAge)
}

// TestOLAPQuery_IgnoreAlias 测试忽略 SQL 别名，直接取第一个值
func TestOLAPQuery_IgnoreAlias(t *testing.T) {
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

	// 插入测试数据
	testUsers := []*TestUser{
		{Username: "alias_user1", Email: "alias1@example.com", Age: 25},
		{Username: "alias_user2", Email: "alias2@example.com", Age: 35},
	}

	for _, user := range testUsers {
		err := repo.Save(user)
		if err != nil {
			t.Fatalf("保存用户失败: %v", err)
		}
	}

	// 测试带别名的 COUNT，应该忽略别名，直接取第一个值
	var returnType int64
	results := db.ExecuteQuery("SELECT COUNT(*) as total_records, MAX(age) as max_age FROM test_user", [][]any{}, returnType)

	if len(results) == 0 {
		t.Fatal("查询应该返回至少一个结果")
	}

	// 应该只返回第一个值（COUNT），忽略第二个值（MAX）
	count, ok := results[0].(int64)
	if !ok {
		t.Fatalf("期望返回 int64，实际类型: %T, 值: %v", results[0], results[0])
	}

	if count < 2 {
		t.Errorf("期望至少 2 条记录，实际: %d", count)
	}

	t.Logf("忽略别名测试成功: 返回第一个值 %d（COUNT），忽略第二个值（MAX）", count)
}

// TestOLAPQuery_EmptyTable 测试空表的 COUNT 查询
func TestOLAPQuery_EmptyTable(t *testing.T) {
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

	// 测试空表的 COUNT
	var returnType int64
	results := db.ExecuteQuery("SELECT COUNT(*) as cnt FROM test_user WHERE username = 'non_existent_user'", [][]any{}, returnType)

	if len(results) == 0 {
		t.Fatal("COUNT 查询应该返回至少一个结果（即使是 0）")
	}

	count, ok := results[0].(int64)
	if !ok {
		t.Fatalf("期望返回 int64，实际类型: %T, 值: %v", results[0], results[0])
	}

	if count != 0 {
		t.Errorf("期望 COUNT = 0，实际: %d", count)
	}

	t.Logf("空表 COUNT 查询成功: 返回 %d", count)
}

// TestOLAPQuery_WithParams 测试带参数的 OLAP 查询
func TestOLAPQuery_WithParams(t *testing.T) {
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

	// 插入测试数据
	testUsers := []*TestUser{
		{Username: "param_user1", Email: "param1@example.com", Age: 20},
		{Username: "param_user2", Email: "param2@example.com", Age: 30},
		{Username: "param_user3", Email: "param3@example.com", Age: 40},
	}

	for _, user := range testUsers {
		err := repo.Save(user)
		if err != nil {
			t.Fatalf("保存用户失败: %v", err)
		}
	}

	// 测试带参数的 COUNT
	var returnType int64
	results := db.ExecuteQuery("SELECT COUNT(*) as cnt FROM test_user WHERE age > ?", [][]any{{25}}, returnType)

	if len(results) == 0 {
		t.Fatal("COUNT 查询应该返回至少一个结果")
	}

	count, ok := results[0].(int64)
	if !ok {
		t.Fatalf("期望返回 int64，实际类型: %T, 值: %v", results[0], results[0])
	}

	// age > 25 应该有 2 条记录（30 和 40）
	if count != 2 {
		t.Errorf("期望 COUNT = 2（age > 25），实际: %d", count)
	}

	t.Logf("带参数的 COUNT 查询成功: 返回 %d", count)
}
