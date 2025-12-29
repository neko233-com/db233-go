package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/neko233-com/db233-go/pkg/db233"
)

// User 实体
type User struct {
	ID        int       `db:"id,primary_key,auto_increment"`
	Username  string    `db:"username"`
	Email     string    `db:"email"`
	Age       int       `db:"age"`
	CreatedAt time.Time `db:"created_at"`
}

func main() {
	fmt.Println("=== db233-go 完整示例 ===")

	// 1. 配置管理
	fmt.Println("\n1. 配置管理")
	setupConfig()

	// 2. 日志系统
	fmt.Println("\n2. 日志系统")
	setupLogging()

	// 3. 数据库连接
	fmt.Println("\n3. 数据库连接")
	db := setupDatabase()
	if db == nil {
		log.Fatal("数据库连接失败")
	}
	defer db.Close()

	// 4. 实体管理
	fmt.Println("\n4. 实体管理")
	setupEntities()

	// 5. CRUD 操作
	fmt.Println("\n5. CRUD 操作")
	demonstrateCRUD(db)

	// 6. 事务管理
	fmt.Println("\n6. 事务管理")
	demonstrateTransactions(db)

	// 7. 健康检查
	fmt.Println("\n7. 健康检查")
	demonstrateHealthCheck(db)

	// 8. 数据迁移
	fmt.Println("\n8. 数据迁移")
	demonstrateMigrations(db)

	fmt.Println("\n=== 示例完成 ===")
}

func setupConfig() {
	cm := db233.GetConfigManager()

	// 设置配置
	cm.Set("database.host", "127.0.0.1")
	cm.Set("database.port", 3306)
	cm.Set("database.user", "root")
	cm.Set("database.password", "root")
	cm.Set("database.database", "db233_demo")

	fmt.Printf("配置已设置: host=%s, port=%d\n",
		db233.GetConfigString("database.host", ""),
		db233.GetConfigInt("database.port", 0))
}

func setupLogging() {
	logger := db233.GetLogger()
	logger.SetLevel(db233.INFO)

	db233.LogInfo("日志系统已初始化")
	db233.LogDebug("这是一条调试信息（可能不会显示）")
}

func setupDatabase() *db233.Db {
	// 创建数据库连接
	dataSource, err := sql.Open("mysql",
		fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true",
			db233.GetConfigString("database.user", "root"),
			db233.GetConfigString("database.password", "root"),
			db233.GetConfigString("database.host", "127.0.0.1"),
			db233.GetConfigInt("database.port", 3306),
			db233.GetConfigString("database.database", "db233_demo")))

	if err != nil {
		db233.LogError("创建数据库连接失败: %v", err)
		return nil
	}

	// 测试连接
	if err := dataSource.Ping(); err != nil {
		db233.LogError("数据库连接测试失败: %v", err)
		return nil
	}

	db := db233.NewDb(dataSource, 0, nil)
	db233.LogInfo("数据库连接成功")
	return db
}

func setupEntities() {
	cm := db233.GetCrudManagerInstance()
	cm.AutoInitEntity(&User{})
	db233.LogInfo("User 实体已注册")
}

func demonstrateCRUD(db *db233.Db) {
	repo := db233.NewBaseCrudRepository(db)

	// 创建用户
	user := &User{
		Username:  "demo_user",
		Email:     "demo@example.com",
		Age:       25,
		CreatedAt: time.Now(),
	}

	err := repo.Save(user)
	if err != nil {
		db233.LogError("保存用户失败: %v", err)
		return
	}
	db233.LogInfo("用户已保存，ID: %d", user.ID)

	// 查找用户
	found, err := repo.FindById(user.ID, &User{})
	if err != nil {
		db233.LogError("查找用户失败: %v", err)
		return
	}

	if u, ok := found.(*User); ok {
		db233.LogInfo("找到用户: %s (%s)", u.Username, u.Email)
	}

	// 更新用户
	u := found.(*User)
	u.Age = 26
	err = repo.Update(u)
	if err != nil {
		db233.LogError("更新用户失败: %v", err)
		return
	}
	db233.LogInfo("用户年龄已更新为: %d", u.Age)

	// 查询所有用户
	users, err := repo.FindAll(&User{})
	if err != nil {
		db233.LogError("查询所有用户失败: %v", err)
		return
	}
	db233.LogInfo("总用户数: %d", len(users))

	// 删除用户
	err = repo.DeleteById(user.ID, &User{})
	if err != nil {
		db233.LogError("删除用户失败: %v", err)
		return
	}
	db233.LogInfo("用户已删除")
}

func demonstrateTransactions(db *db233.Db) {
	tm := db233.NewTransactionManager(db)

	err := tm.ExecuteInTransaction(func(tm *db233.TransactionManager) error {
		// 在事务中插入用户
		_, err := tm.Exec("INSERT INTO users (username, email, age, created_at) VALUES (?, ?, ?, ?)",
			"tx_user", "tx@example.com", 30, time.Now())
		if err != nil {
			return err
		}

		// 创建保存点
		err = tm.Savepoint("after_insert")
		if err != nil {
			return err
		}

		// 模拟另一个操作
		_, err = tm.Exec("UPDATE users SET age = age + 1 WHERE username = ?", "tx_user")
		if err != nil {
			return err
		}

		db233.LogInfo("事务操作完成")
		return nil
	})

	if err != nil {
		db233.LogError("事务执行失败: %v", err)
	} else {
		db233.LogInfo("事务执行成功")
	}
}

func demonstrateHealthCheck(db *db233.Db) {
	hc := db233.NewHealthChecker(db)

	// 基本健康检查
	result := hc.Check()
	if result.Healthy {
		db233.LogInfo("健康检查通过: %s (响应时间: %v)", result.Message, result.ResponseTime)
	} else {
		db233.LogWarn("健康检查失败: %s", result.Message)
	}

	// 连接池健康检查
	poolResult := hc.CheckConnectionPool()
	if poolResult.Healthy {
		db233.LogInfo("连接池健康: %s", poolResult.Message)
	} else {
		db233.LogWarn("连接池不健康: %s", poolResult.Message)
	}
}

func demonstrateMigrations(db *db233.Db) {
	// 注意：这个示例假设迁移文件已存在
	// 在实际使用中，你需要先创建迁移文件

	mm := db233.NewMigrationManager(db, "./migrations")

	// 初始化迁移表
	err := mm.Init()
	if err != nil {
		db233.LogError("初始化迁移管理器失败: %v", err)
		return
	}

	// 获取当前版本
	version, err := mm.GetCurrentVersion()
	if err != nil {
		db233.LogError("获取当前版本失败: %v", err)
		return
	}

	db233.LogInfo("当前迁移版本: %d", version)

	// 获取迁移状态
	migrations, err := mm.GetStatus()
	if err != nil {
		db233.LogError("获取迁移状态失败: %v", err)
		return
	}

	db233.LogInfo("发现 %d 个迁移文件", len(migrations))
	for _, m := range migrations {
		status := "未应用"
		if m.AppliedAt != nil {
			status = "已应用"
		}
		fmt.Printf("  - %d_%s: %s\n", m.Version, m.Name, status)
	}
}
