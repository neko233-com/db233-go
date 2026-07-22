package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/neko233-com/db233-go/pkg/db233"
)

type transactionAutoIncrementEntity struct {
	ID   int64  `db:"id" primary_key:"true" auto_increment:"true"`
	Name string `db:"name"`
}

func (*transactionAutoIncrementEntity) TableName() string       { return "test_transaction_auto_step" }
func (*transactionAutoIncrementEntity) SerializeBeforeSaveDb()  {}
func (*transactionAutoIncrementEntity) DeserializeAfterLoadDb() {}

// TestTransactionAutoIncrementSessionStepIntegration 验证真实 MySQL 会话步长与事务提交后 ID 回填一致。
// 本地 MySQL 不可用时复用 CreateTestDb 的跳过语义。
func TestTransactionAutoIncrementSessionStepIntegration(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	// 会话变量只影响单个连接；限制为一条连接，保证建表、事务和恢复都落在同一会话。
	db.DataSource.SetMaxOpenConns(1)
	db.DataSource.SetMaxIdleConns(1)

	var originalIncrement, originalOffset int64
	if err := db.DataSource.QueryRow(
		"SELECT @@SESSION.auto_increment_increment, @@SESSION.auto_increment_offset",
	).Scan(&originalIncrement, &originalOffset); err != nil {
		t.Skipf("无法读取 MySQL 自增会话变量: %v", err)
	}

	if _, err := db.DataSource.Exec("DROP TABLE IF EXISTS test_transaction_auto_step"); err != nil {
		t.Fatalf("清理测试表失败: %v", err)
	}
	if _, err := db.DataSource.Exec(`
		CREATE TABLE test_transaction_auto_step (
			id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(64) NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`); err != nil {
		t.Fatalf("创建测试表失败: %v", err)
	}

	var tm *db233.TransactionManager
	defer func() {
		if tm != nil && tm.IsActive() {
			_ = tm.Rollback()
		}
		_, _ = db.DataSource.Exec(fmt.Sprintf("SET SESSION auto_increment_increment = %d", originalIncrement))
		_, _ = db.DataSource.Exec(fmt.Sprintf("SET SESSION auto_increment_offset = %d", originalOffset))
		_, _ = db.DataSource.Exec("DROP TABLE IF EXISTS test_transaction_auto_step")
	}()

	tm = db233.NewTransactionManager(db)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tm.BeginContext(ctx); err != nil {
		t.Fatalf("开始事务失败: %v", err)
	}
	if _, err := tm.ExecContext(ctx, "SET SESSION auto_increment_increment = 2"); err != nil {
		t.Skipf("当前 MySQL 不允许设置 auto_increment_increment: %v", err)
	}
	if _, err := tm.ExecContext(ctx, "SET SESSION auto_increment_offset = 1"); err != nil {
		t.Skipf("当前 MySQL 不允许设置 auto_increment_offset: %v", err)
	}

	repository, err := tm.CrudRepository()
	if err != nil {
		t.Fatalf("创建事务 Repository 失败: %v", err)
	}
	first := &transactionAutoIncrementEntity{Name: "first"}
	second := &transactionAutoIncrementEntity{Name: "second"}
	if err := repository.SaveBatchUpsertContext(ctx, []db233.IDbEntity{first, second}); err != nil {
		t.Fatalf("事务批量保存失败: %v", err)
	}
	if err := tm.Commit(); err != nil {
		t.Fatalf("提交事务失败: %v", err)
	}
	if first.ID <= 0 || second.ID-first.ID != 2 {
		t.Fatalf("回填 ID=(%d,%d)，期望正数且步长为 2", first.ID, second.ID)
	}

	rows, err := db.DataSource.Query("SELECT id FROM test_transaction_auto_step ORDER BY id")
	if err != nil {
		t.Fatalf("读取真实 ID 失败: %v", err)
	}
	defer rows.Close()
	want := []int64{first.ID, second.ID}
	for index := 0; index < len(want); index++ {
		if !rows.Next() {
			t.Fatalf("数据库仅返回 %d 个 ID，期望 %d 个", index, len(want))
		}
		var got int64
		if err := rows.Scan(&got); err != nil {
			t.Fatalf("扫描真实 ID 失败: %v", err)
		}
		if got != want[index] {
			t.Fatalf("数据库 ID[%d]=%d，回填值=%d", index, got, want[index])
		}
	}
	if rows.Next() {
		t.Fatal("数据库返回了额外记录")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历真实 ID 失败: %v", err)
	}
}
