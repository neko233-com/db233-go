package tests

import (
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

// TestUpsertEntity 用于测试 upsert 的实体
type TestUpsertEntity struct {
	AccountID string `json:"accountId" db:"accountId" primary_key:"true"`
	PlayerID  string `json:"playerId" db:"playerId"`
	Password  string `json:"password" db:"password"`
	Email     string `json:"email" db:"email"`
}

func (e *TestUpsertEntity) TableName() string {
	return "test_upsert"
}

func (e *TestUpsertEntity) GetDbUid() string {
	return "accountId"
}

func (e *TestUpsertEntity) SerializeBeforeSaveDb() {}

func (e *TestUpsertEntity) DeserializeAfterLoadDb() {}

// TestUpsertSave 测试 upsert 功能
func TestUpsertSave(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 创建测试表
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS test_upsert (
			accountId VARCHAR(255) NOT NULL PRIMARY KEY,
			playerId VARCHAR(255) NULL,
			password VARCHAR(255) NULL,
			email VARCHAR(255) NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`
	_, err := db.DataSource.Exec(createTableSQL)
	if err != nil {
		t.Fatalf("创建测试表失败: %v", err)
	}
	defer func() {
		db.DataSource.Exec("DROP TABLE IF EXISTS test_upsert")
	}()

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUpsertEntity{})

	// 第一次保存（INSERT）
	entity1 := &TestUpsertEntity{
		AccountID: "test_account_001",
		PlayerID:  "player_001",
		Password:  "password123",
		Email:     "test1@example.com",
	}

	err = repo.Save(entity1)
	if err != nil {
		t.Fatalf("第一次保存失败: %v", err)
	}

	// 验证第一次保存
	found1, err := repo.FindById("test_account_001", &TestUpsertEntity{})
	if err != nil {
		t.Fatalf("查询第一次保存的数据失败: %v", err)
	}
	if found1 == nil {
		t.Fatal("应该找到第一次保存的数据")
	}
	foundEntity1 := found1.(*TestUpsertEntity)
	if foundEntity1.PlayerID != "player_001" {
		t.Errorf("PlayerID 应该是 'player_001'，得到: %s", foundEntity1.PlayerID)
	}
	if foundEntity1.Email != "test1@example.com" {
		t.Errorf("Email 应该是 'test1@example.com'，得到: %s", foundEntity1.Email)
	}

	// 第二次保存相同的主键（应该 UPDATE 而不是报错）
	entity2 := &TestUpsertEntity{
		AccountID: "test_account_001", // 相同的主键
		PlayerID:  "player_002",        // 不同的值
		Password:  "newpassword",       // 不同的值
		Email:     "test2@example.com", // 不同的值
	}

	err = repo.Save(entity2)
	if err != nil {
		t.Fatalf("第二次保存（upsert）失败: %v", err)
	}

	// 验证第二次保存（应该更新了非主键字段）
	found2, err := repo.FindById("test_account_001", &TestUpsertEntity{})
	if err != nil {
		t.Fatalf("查询第二次保存的数据失败: %v", err)
	}
	if found2 == nil {
		t.Fatal("应该找到第二次保存的数据")
	}
	foundEntity2 := found2.(*TestUpsertEntity)
	if foundEntity2.AccountID != "test_account_001" {
		t.Errorf("AccountID 应该仍然是 'test_account_001'，得到: %s", foundEntity2.AccountID)
	}
	if foundEntity2.PlayerID != "player_002" {
		t.Errorf("PlayerID 应该已更新为 'player_002'，得到: %s", foundEntity2.PlayerID)
	}
	if foundEntity2.Password != "newpassword" {
		t.Errorf("Password 应该已更新为 'newpassword'，得到: %s", foundEntity2.Password)
	}
	if foundEntity2.Email != "test2@example.com" {
		t.Errorf("Email 应该已更新为 'test2@example.com'，得到: %s", foundEntity2.Email)
	}

	t.Logf("Upsert 测试通过: 成功执行 INSERT 和 UPDATE")
}

// TestAutoIncrementEntity 测试自增主键实体
type TestAutoIncrementEntity struct {
	ID    int    `db:"id,primary_key,auto_increment"`
	Name  string `db:"name"`
	Value string `db:"value"`
}

func (e *TestAutoIncrementEntity) TableName() string {
	return "test_upsert_auto"
}

func (e *TestAutoIncrementEntity) GetDbUid() string {
	return "id"
}

func (e *TestAutoIncrementEntity) SerializeBeforeSaveDb() {}

func (e *TestAutoIncrementEntity) DeserializeAfterLoadDb() {}

// TestUpsertWithAutoIncrement 测试自增主键的 upsert
func TestUpsertWithAutoIncrement(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 创建测试表（自增主键）
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS test_upsert_auto (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NULL,
			value VARCHAR(255) NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`
	_, err := db.DataSource.Exec(createTableSQL)
	if err != nil {
		t.Fatalf("创建测试表失败: %v", err)
	}
	defer func() {
		db.DataSource.Exec("DROP TABLE IF EXISTS test_upsert_auto")
	}()

	entity := &TestAutoIncrementEntity{
		Name:  "test_name",
		Value: "test_value",
	}

	repo := db233.NewBaseCrudRepository(db)
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(entity)

	// 第一次保存（自增主键，应该使用普通 INSERT）
	err = repo.Save(entity)
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	if entity.ID == 0 {
		t.Error("自增主键应该被设置")
	}

	originalID := entity.ID

	// 第二次保存（没有主键值，应该使用普通 INSERT，创建新记录）
	entity2 := &TestAutoIncrementEntity{
		Name:  "test_name_2",
		Value: "test_value_2",
	}

	err = repo.Save(entity2)
	if err != nil {
		t.Fatalf("第二次保存失败: %v", err)
	}

	if entity2.ID == originalID {
		t.Error("第二次保存应该创建新记录，ID 应该不同")
	}

	t.Logf("自增主键 upsert 测试通过: 第一次 ID=%d, 第二次 ID=%d", originalID, entity2.ID)
}

