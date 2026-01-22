package tests

import (
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

// TestUserWithIndex 带索引的测试用户
type TestUserWithIndex struct {
	ID        int    `db:"id" primary_key:"true" auto_increment:"true"`
	PlayerID  int64  `db:"playerId"`
	AccountID string `db:"accountId"`
	Username  string `db:"username"`
	Email     string `db:"email"`
	Age       int    `db:"age"`
}

func (u *TestUserWithIndex) TableName() string {
	return "test_user_index"
}

func (u *TestUserWithIndex) SerializeBeforeSaveDb() {
}

func (u *TestUserWithIndex) DeserializeAfterLoadDb() {
}

// GetTableMetaData 实现 ITableMetaDataProvider 接口，提供索引元数据
func (u *TestUserWithIndex) GetTableMetaData() *db233.TableMetaData {
	return db233.NewIndexBuilder("test_user_index").
		AddNewIndexName("index_playerId").
		AddIndexColumn("playerId").
		DoneIndex().
		AddNewIndexName("index_accountId_playerId").
		AddIndexColumn("accountId").
		AddIndexColumn("playerId").
		DoneIndex().
		Build()
}

// TestIndexMigration 测试索引迁移
func TestIndexMigration(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 清理测试表
	_, _ = db.DataSource.Exec("DROP TABLE IF EXISTS test_user_index")

	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&TestUserWithIndex{})

	// 第一次迁移：创建表和索引
	err := cm.AutoMigrateTableSimple(db, &TestUserWithIndex{})
	if err != nil {
		t.Fatalf("第一次迁移失败: %v", err)
	}

	// 验证索引是否创建
	strategy := db233.GetStrategyFactoryInstance().GetStrategy(db.DatabaseType)
	indexes, err := strategy.GetExistingIndexes(db, "test_user_index")
	if err != nil {
		t.Fatalf("获取索引失败: %v", err)
	}

	// 应该有两个索引
	if len(indexes) < 2 {
		t.Errorf("期望至少 2 个索引，实际: %d", len(indexes))
	}

	// 验证索引名称
	if _, exists := indexes["index_playerId"]; !exists {
		t.Error("索引 index_playerId 不存在")
	}
	if _, exists := indexes["index_accountId_playerId"]; !exists {
		t.Error("索引 index_accountId_playerId 不存在")
	}

	t.Logf("索引迁移测试通过: 找到 %d 个索引", len(indexes))
}

// TestIndexBuilder 测试索引构建器
func TestIndexBuilder(t *testing.T) {
	tableName := "test_table"
	builder := db233.NewIndexBuilder(tableName)

	metaData := builder.
		AddNewIndexName("index_playerId").
		AddIndexColumn("playerId").
		DoneIndex().
		AddNewIndexName("index_accountId_playerId").
		AddIndexColumn("accountId").
		AddIndexColumn("playerId").
		DoneIndex().
		Build()

	if metaData == nil {
		t.Fatal("TableMetaData 不应该为 nil")
	}

	if metaData.TableName != tableName {
		t.Errorf("表名不匹配: 期望=%s, 实际=%s", tableName, metaData.TableName)
	}

	if len(metaData.Indexes) != 2 {
		t.Errorf("期望 2 个索引，实际: %d", len(metaData.Indexes))
	}

	// 验证第一个索引
	idx1 := metaData.Indexes[0]
	if idx1.IndexName != "index_playerId" {
		t.Errorf("第一个索引名不匹配: 期望=index_playerId, 实际=%s", idx1.IndexName)
	}
	if len(idx1.Columns) != 1 || idx1.Columns[0] != "playerId" {
		t.Errorf("第一个索引列不匹配: 期望=[playerId], 实际=%v", idx1.Columns)
	}

	// 验证第二个索引
	idx2 := metaData.Indexes[1]
	if idx2.IndexName != "index_accountId_playerId" {
		t.Errorf("第二个索引名不匹配: 期望=index_accountId_playerId, 实际=%s", idx2.IndexName)
	}
	if len(idx2.Columns) != 2 || idx2.Columns[0] != "accountId" || idx2.Columns[1] != "playerId" {
		t.Errorf("第二个索引列不匹配: 期望=[accountId playerId], 实际=%v", idx2.Columns)
	}

	t.Logf("索引构建器测试通过: 表名=%s, 索引数=%d", metaData.TableName, len(metaData.Indexes))
}

// TestIndexBuilderUnique 测试唯一索引
func TestIndexBuilderUnique(t *testing.T) {
	tableName := "test_table"
	builder := db233.NewIndexBuilder(tableName)

	metaData := builder.
		AddNewIndexName("unique_email").
		AddIndexColumn("email").
		SetUnique(true).
		DoneIndex().
		Build()

	if len(metaData.Indexes) != 1 {
		t.Fatalf("期望 1 个索引，实际: %d", len(metaData.Indexes))
	}

	idx := metaData.Indexes[0]
	if !idx.IsUnique {
		t.Error("索引应该是唯一索引")
	}

	t.Logf("唯一索引测试通过: 索引名=%s, 唯一=%v", idx.IndexName, idx.IsUnique)
}
