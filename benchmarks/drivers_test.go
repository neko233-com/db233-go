package benchmarks

import (
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/neko233-com/db233-go/pkg/db233"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func mustSQLX(t *testing.T, dsn string) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Fatalf("sqlx 连接失败: %v", err)
	}
	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(10)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("关闭 sqlx 连接失败: %s", db233.SafeErrorSummary(err))
		}
	})
	return db
}

func mustGORM(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm 连接失败: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetMaxIdleConns(10)
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("关闭 GORM 连接失败: %s", db233.SafeErrorSummary(err))
		}
	})
	return db
}

// GORM 模型
type gormPlayer struct {
	PlayerID string `gorm:"column:playerId;primaryKey"`
	Name     string `gorm:"column:name"`
	Level    int    `gorm:"column:level"`
}

func (gormPlayer) TableName() string { return benchTable }

type gormBag struct {
	PlayerID string `gorm:"column:playerId;primaryKey"`
	Gold     int    `gorm:"column:gold"`
}

func (gormBag) TableName() string { return benchBagTable }

type gormQuest struct {
	PlayerID  string `gorm:"column:playerId;primaryKey"`
	QuestData string `gorm:"column:questData"`
}

func (gormQuest) TableName() string { return benchQuestTable }

type sqlxPlayer struct {
	PlayerID string `db:"playerId"`
	Name     string `db:"name"`
	Level    int    `db:"level"`
}

type sqlxBag struct {
	PlayerID string `db:"playerId"`
	Gold     int    `db:"gold"`
}

type sqlxQuest struct {
	PlayerID  string `db:"playerId"`
	QuestData string `db:"questData"`
}
