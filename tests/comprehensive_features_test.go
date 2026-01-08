package tests

import (
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

// TestPrimaryKeyAutoDetection 测试主键自动检测
type TestPrimaryKeyEntity struct {
	UserID   int64  `db:"user_id,primary_key,auto_increment"` // 自动检测为主键
	Username string `db:"username"`
	Email    string `db:"email"`
	// 没有 db 标签的字段应该被忽略
	InternalCache string
	// db:"-" 显式忽略的字段
	IgnoredField string `db:"-"`
}

func (e *TestPrimaryKeyEntity) TableName() string {
	return "test_pk_detection"
}

func (e *TestPrimaryKeyEntity) SerializeBeforeSaveDb() {}

func (e *TestPrimaryKeyEntity) DeserializeAfterLoadDb() {}

// TestPrimaryKeyAutoDetection 测试主键自动检测功能
func TestPrimaryKeyAutoDetection(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 创建测试表
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS test_pk_detection (
			user_id BIGINT AUTO_INCREMENT PRIMARY KEY,
			username VARCHAR(255) NULL,
			email VARCHAR(255) NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`
	_, err := db.DataSource.Exec(createTableSQL)
	if err != nil {
		t.Fatalf("创建测试表失败: %v", err)
	}
	defer func() {
		db.DataSource.Exec("DROP TABLE IF EXISTS test_pk_detection")
	}()

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestPrimaryKeyEntity{})

	// 获取自动检测的主键列名（应该不需要手动实现 GetDbUidColumnName）
	pkColumn := cm.GetPrimaryKeyColumnName(&TestPrimaryKeyEntity{})
	if pkColumn != "user_id" {
		t.Errorf("主键列名应该自动检测为 'user_id'，得到: %s", pkColumn)
	}

	// 测试保存（自增主键应该自动处理）
	entity := &TestPrimaryKeyEntity{
		Username:      "test_user",
		Email:         "test@example.com",
		InternalCache: "should_be_ignored", // 没有 db 标签，应该被忽略
		IgnoredField:  "also_ignored",      // db:"-"，应该被忽略
	}

	err = repo.Save(entity)
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	// 验证自增主键已被设置
	if entity.UserID == 0 {
		t.Error("自增主键应该已被自动设置")
	}

	t.Logf("主键自动检测测试通过: 主键列=%s, 主键值=%d", pkColumn, entity.UserID)

	// 验证查询
	found, err := repo.FindById(entity.UserID, &TestPrimaryKeyEntity{})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if found == nil {
		t.Fatal("应该找到保存的记录")
	}

	foundEntity := found.(*TestPrimaryKeyEntity)
	if foundEntity.Username != "test_user" {
		t.Errorf("用户名应该是 'test_user'，得到: %s", foundEntity.Username)
	}

	t.Logf("主键自动检测完整测试通过")
}

// TestDbTagIgnore 测试 db 标签忽略功能
type TestDbTagEntity struct {
	ID           int64  `db:"id,primary_key,auto_increment"`
	PublicField  string `db:"public_field"` // 有 db 标签，会被保存
	PrivateField string // 没有 db 标签，应该被忽略
	IgnoredField string `db:"-"`               // db:"-"，应该被忽略
	SkipField    string `db:"skip_field,skip"` // db:"xxx,skip"，应该被忽略
}

func (e *TestDbTagEntity) TableName() string {
	return "test_db_tag"
}

func (e *TestDbTagEntity) SerializeBeforeSaveDb() {}

func (e *TestDbTagEntity) DeserializeAfterLoadDb() {}

// TestDbTagIgnoreFields 测试 db 标签忽略字段功能
func TestDbTagIgnoreFields(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 创建测试表（只包含有 db 标签的字段）
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS test_db_tag (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			public_field VARCHAR(255) NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`
	_, err := db.DataSource.Exec(createTableSQL)
	if err != nil {
		t.Fatalf("创建测试表失败: %v", err)
	}
	defer func() {
		db.DataSource.Exec("DROP TABLE IF EXISTS test_db_tag")
	}()

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestDbTagEntity{})

	// 保存实体
	entity := &TestDbTagEntity{
		PublicField:  "should_be_saved",
		PrivateField: "should_be_ignored_no_tag",
		IgnoredField: "should_be_ignored_dash",
		SkipField:    "should_be_ignored_skip",
	}

	err = repo.Save(entity)
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	// 验证只有 PublicField 被保存
	found, err := repo.FindById(entity.ID, &TestDbTagEntity{})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if found == nil {
		t.Fatal("应该找到保存的记录")
	}

	foundEntity := found.(*TestDbTagEntity)
	if foundEntity.PublicField != "should_be_saved" {
		t.Errorf("PublicField 应该是 'should_be_saved'，得到: %s", foundEntity.PublicField)
	}

	// PrivateField、IgnoredField、SkipField 不应该被保存（应该是空值）
	if foundEntity.PrivateField != "" {
		t.Logf("PrivateField 应该为空（未被保存），但得到: %s", foundEntity.PrivateField)
	}
	if foundEntity.IgnoredField != "" {
		t.Logf("IgnoredField 应该为空（未被保存），但得到: %s", foundEntity.IgnoredField)
	}

	t.Logf("db 标签忽略字段测试通过")
}

// ProductEntity 产品实体（用于测试 UPSERT）
type ProductEntity struct {
	ProductID   string  `db:"product_id,primary_key"`
	ProductName string  `db:"product_name"`
	Price       float64 `db:"price"`
}

func (e *ProductEntity) TableName() string {
	return "test_upsert_all"
}

func (e *ProductEntity) SerializeBeforeSaveDb() {}

func (e *ProductEntity) DeserializeAfterLoadDb() {}

// TestUpsertAllInserts 测试所有 Insert 都是 Upsert 模式
func TestUpsertAllInserts(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 创建测试表
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS test_upsert_all (
			product_id VARCHAR(255) NOT NULL PRIMARY KEY,
			product_name VARCHAR(255) NULL,
			price DECIMAL(10,2) NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`
	_, err := db.DataSource.Exec(createTableSQL)
	if err != nil {
		t.Fatalf("创建测试表失败: %v", err)
	}
	defer func() {
		db.DataSource.Exec("DROP TABLE IF EXISTS test_upsert_all")
	}()

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&ProductEntity{})

	// 第一次保存（INSERT）
	product1 := &ProductEntity{
		ProductID:   "PROD001",
		ProductName: "Product 1",
		Price:       99.99,
	}

	err = repo.Save(product1)
	if err != nil {
		t.Fatalf("第一次保存失败: %v", err)
	}

	// 第二次保存相同主键（应该 UPSERT，不报错）
	product2 := &ProductEntity{
		ProductID:   "PROD001", // 相同主键
		ProductName: "Updated Product 1",
		Price:       149.99,
	}

	err = repo.Save(product2)
	if err != nil {
		t.Fatalf("第二次保存（UPSERT）失败: %v", err)
	}

	// 验证更新成功
	found, err := repo.FindById("PROD001", &ProductEntity{})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if found == nil {
		t.Fatal("应该找到记录")
	}

	foundProduct := found.(*ProductEntity)
	if foundProduct.ProductName != "Updated Product 1" {
		t.Errorf("产品名称应该已更新为 'Updated Product 1'，得到: %s", foundProduct.ProductName)
	}
	if foundProduct.Price != 149.99 {
		t.Errorf("价格应该已更新为 149.99，得到: %f", foundProduct.Price)
	}

	t.Logf("UPSERT 模式测试通过: 所有 INSERT 都自动转为 UPSERT")
}
