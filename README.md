# db233-go

> 🚀 **v1.1.0 重大更新：** 现在支持类似 Java JPA 的实体继承机制！通过结构体嵌入实现继承，减少 90% 的模板代码。

db233-go 是 db233 的 Go 语言版本，一个功能强大的数据库操作库，提供 ORM、分片、迁移和监控功能。

## 📋 目录

- [核心特性](#特性)
- [快速开始](#快速开始)
  - [普通实体定义](#方式-1普通实体)
  - [JPA 风格实体继承](#方式-2jpa-风格实体继承--推荐) ⭐ 推荐
  - [CRUD 操作](#3-使用-crud-操作)
  - [自动建表和迁移](#4-自动建表和表结构迁移)
- [JPA 继承完整指南](#jpa-风格实体继承完整指南)
- [高级特性](#高级特性)
- [API 文档](#api-文档)
- [贡献指南](#贡献)
- [许可证](#许可证)

## ⚡ 快速体验 JPA 继承

**Java JPA 写法 vs DB233-Go 写法：**

<table>
<tr>
<td width="50%">

**Java JPA**
```java
@Entity
public abstract class BasePlayerEntity {
    @Id
    @Column(name = "playerId")
    private Long playerId;
}

@Entity
public class StrengthEntity 
    extends BasePlayerEntity {
    @Column(name = "current_strength")
    private Integer currentStrength;
}
```

</td>
<td width="50%">

**DB233-Go**
```go
type BasePlayerEntity struct {
    PlayerID int64 `db:"playerId,primary_key"`
}

type StrengthEntity struct {
    BasePlayerEntity // 嵌入 = 继承
    CurrentStrength int `db:"current_strength"`
}

// ✅ 自动检测主键，无需 GetDbUid()
// ✅ 自动 UPSERT，避免主键冲突
// ✅ 代码量减少 90%
```

</td>
</tr>
</table>

📖 **详细文档：** [JPA 继承指南（中文）](docs/JPA_INHERITANCE_CN.md) | [完整指南（英文）](docs/JPA_INHERITANCE_GUIDE.md) | [快速参考](docs/QUICK_REFERENCE.md)

---

## 特性

### 核心功能
- **ORM**: 基于反射的自动对象关系映射
- **JPA 风格实体继承** ⭐ NEW！
  - 支持结构体嵌入实现类似 JPA 的实体继承
  - 自动检测父类的 `@Id` (主键) 和 `@Column` (列)
  - 无需手动实现 `GetDbUid()` 方法
  - 详见 [JPA 继承指南](docs/JPA_INHERITANCE_GUIDE.md)
- **UPSERT 自动处理**: 所有 Save 操作自动使用 INSERT...ON DUPLICATE KEY UPDATE
- **字段忽略机制**: 支持 `db:"-"` 和无 db tag 忽略字段
- **分片策略**: 支持多种数据库和表分片策略
- **CRUD 操作**: 简化的数据访问接口
- **连接池**: 高效的数据库连接管理
- **插件系统**: 可扩展的钩子架构，支持监控和自定义逻辑
- **实体缓存**: 线程安全的元数据缓存，提高运行时性能
- **包扫描**: 自动类型发现和注册
- **监控**: 内置性能监控、指标收集和日志记录
- **事务管理**: 支持复杂事务和保存点
- **数据迁移**: 版本控制的数据库模式迁移
- **健康检查**: 数据库连接和连接池健康监控
- **配置管理**: 灵活的配置加载和管理
- **日志系统**: 结构化日志记录

## 安装

```bash
go get github.com/neko233-com/db233-go
```

## 快速开始

### 1. 初始化数据库管理器

```go
package main

import (
    "github.com/neko233-com/db233-go/pkg/db233"
)

func main() {
    // 获取单例实例
    manager := db233.GetInstance()

    // 配置数据库组
    config := &db233.DbGroupConfig{
        GroupName: "myapp",
        DbConfigFetcher: &MyDbConfigFetcher{}, // 实现配置获取器
    }

    // 创建数据库组
    dbGroup, err := db233.NewDbGroup(config)
    if err != nil {
        panic(err)
    }

    // 添加到管理器
    err = manager.AddDbGroup(dbGroup)
    if err != nil {
        panic(err)
    }
}
```

### 2. 定义实体

#### 方式 1：普通实体

```go
type User struct {
    ID       int    `db:"id,primary_key,auto_increment"`
    Username string `db:"username,not_null"`
    Email    string `db:"email"`
    Age      int    `db:"age"`
    Internal string `db:"-"` // 忽略此字段，不会生成数据库列
    // NoTag  string            // 没有 db 标签的字段也会被忽略
}
```

#### 方式 2：JPA 风格实体继承 ⭐ 推荐！

类似 Java JPA 的 `@Entity` 继承机制，减少重复代码：

```go
// 基础实体（父类）
type BasePlayerEntity struct {
    // 主键：自动检测，无需手动实现 GetDbUid()
    // 推荐使用独立的 primary_key 标签（更清晰）
    PlayerID int64 `json:"playerId" db:"playerId" primary_key:"true"`
}

// 业务实体（子类）- 自动继承 playerId 主键
type StrengthEntity struct {
    BasePlayerEntity  // 嵌入父类，类似 Java 的 extends
    
    CurrentStrength int   `db:"current_strength"`
    UpdatedAt       int64 `db:"updated_at"`
    
    // 忽略字段
    CachedValue string `db:"-"`        // 不存储
    NoDbTag     string                 // 无 db tag，也不存储
}

// 实现 IDbEntity 接口
func (e *StrengthEntity) TableName() string {
    return "StrengthEntity"
}

func (e *StrengthEntity) SerializeBeforeSaveDb() {}
func (e *StrengthEntity) DeserializeAfterLoadDb() {}
```

**主键定义的两种风格（都支持）：**

1. **独立标签风格（推荐）：** `primary_key:"true"`
   ```go
   PlayerID int64 `json:"playerId" db:"playerId" primary_key:"true"`
   ```

2. **逗号分隔风格：** `db:"playerId,primary_key"`
   ```go
   PlayerID int64 `json:"playerId" db:"playerId,primary_key"`
   ```

**优势：**
- ✅ 自动继承父类的主键字段 (`playerId`)
- ✅ 自动继承父类的业务方法 (`GetPlayerID()`, `SetPlayerID()`)
- ✅ 无需手动实现 `GetDbUid()` 方法
- ✅ 支持多层继承（BaseEntity -> BasePlayerEntity -> StrengthEntity）

详细说明请参考：[JPA 继承指南](docs/JPA_INHERITANCE_GUIDE.md)

---

**重要说明：**
- 字段必须有 `db` 标签才会被处理和映射到数据库列
- 使用 `db:"-"` 可以明确忽略字段，不会在数据库中创建对应的列
- 没有 `db` 标签的字段会被自动忽略
- `db` 标签格式：`db:"列名,选项1,选项2"`
  - 列名：数据库列名
  - 选项：`primary_key`（主键）、`auto_increment`（自增）、`not_null`（非空）、`skip`（跳过）
  
**支持的主键定义方式：**
1. **独立标签（推荐）：** `primary_key:"true"`
   ```go
   PlayerID int64 `db:"playerId" primary_key:"true"`
   ```
2. **逗号分隔：** `db:"列名,primary_key"`
   ```go
   PlayerID int64 `db:"playerId,primary_key"`
   ```
3. **字段名约定：** 字段名为 `ID` 或 `Id` 会自动识别为主键

**支持的 db 标签选项：**
  - `db:"column_name"` - 指定列名
  - `db:"column_name,primary_key"` - 主键（或使用 `primary_key:"true"`）
  - `db:"column_name,auto_increment"` - 自增主键（零值会被跳过，由数据库生成）
  - `db:"column_name,not_null"` - 非空约束
  - `db:"-"` - 忽略字段

**⚠️ 主键字段的特殊处理：**
- 如果主键字段的值为**零值**（int 类型为 0，string 类型为 ""），该字段会被**自动跳过**，不包含在 INSERT 语句中
- 这适用于自增主键场景（`auto_increment`），让数据库自动生成主键值
- 如果你需要手动设置主键值（非自增主键），**必须确保主键字段的值不为零值**
- 示例：
  ```go
  // ❌ 错误：RankId 为 0，会被跳过，导致 "Field 'rankId' doesn't have a default value" 错误
  entity := &RankEntity{
      RankId: 0,  // 零值，会被跳过
      RankName: "test",
  }
  
  // ✅ 正确：RankId 有非零值，会被包含在 INSERT 语句中
  entity := &RankEntity{
      RankId: 1001,  // 非零值，会被包含
      RankName: "test",
  }
  
  // ✅ 或者使用自增主键（让数据库生成）
  type RankEntity struct {
      RankId int `db:"rankId,primary_key,auto_increment"` // 添加 auto_increment
  }
  ```

### 3. 使用 CRUD 操作

```go
// 初始化实体元数据
crudManager := db233.GetCrudManagerInstance()
crudManager.AutoInitEntity(&User{})

// 创建存储库
db, _ := manager.GetDb("myapp", 0) // 获取数据库实例
repo := &db233.BaseCrudRepository{Db: db}

// 保存用户
user := &User{
    Username: "john_doe",
    Email:    "john@example.com",
    Age:      30,
}

err := repo.Save(user)
if err != nil {
    log.Printf("保存失败: %v", err)
}

// 查找用户
found, err := repo.FindById(1, &User{})
if err != nil {
    log.Printf("查找失败: %v", err)
}
```

**UPSERT 功能（INSERT ... ON DUPLICATE KEY UPDATE）：**

Save 方法会自动处理主键冲突：
- 如果主键不存在，执行 INSERT 操作
- 如果主键已存在，执行 UPDATE 操作（更新除主键外的所有字段）

```go
// 首次保存 - 执行 INSERT
user := &User{
    ID:       1000022,
    Username: "john_doe",
    Email:    "john@example.com",
    Age:      30,
}
err := repo.Save(user) // INSERT INTO users ...

// 再次保存相同主键 - 执行 UPDATE
user.Age = 31
err = repo.Save(user) // INSERT ... ON DUPLICATE KEY UPDATE age=31
// 不会报错 "Duplicate entry '1000022' for key 'PRIMARY'"，而是自动更新
```

### 4. 自动建表和表结构迁移

db233-go 提供强大的自动建表和表结构迁移功能，可以根据实体定义自动创建表或更新表结构。

**自动创建表：**

```go
// 获取 CrudManager 实例
cm := db233.GetCrudManagerInstance()

// 自动创建表（如果表不存在）
err := cm.AutoCreateTable(db, &User{})
if err != nil {
    log.Printf("创建表失败: %v", err)
}
```

**自动迁移表结构：**

```go
// 自动迁移表（创建表或添加缺失的列）
err := cm.AutoMigrateTable(db, &User{})
if err != nil {
    log.Printf("迁移表失败: %v", err)
}
```

**工作原理：**

1. **AutoCreateTable**: 
   - 检查表是否存在
   - 如果不存在，根据实体定义生成 CREATE TABLE SQL
   - 只处理有 `db` 标签的字段
   - 忽略 `db:"-"` 和没有 `db` 标签的字段

2. **AutoMigrateTable**:
   - 检查表是否存在，如果不存在则创建
   - 如果表已存在，比对实体定义和数据库表结构
   - 自动添加缺失的列（不删除已有列，保证数据安全）
   - 支持添加新字段而不影响现有数据

**示例：**

```go
// 定义实体
type User struct {
    ID       int    `db:"id,primary_key,auto_increment"`
    Username string `db:"username,not_null"`
    Email    string `db:"email"`
    Age      int    `db:"age"`
    Internal string `db:"-"` // 不会创建此列
}

// 自动创建表
cm := db233.GetCrudManagerInstance()
err := cm.AutoCreateTable(db, &User{})

// 后续添加新字段
type User struct {
    ID       int    `db:"id,primary_key,auto_increment"`
    Username string `db:"username,not_null"`
    Email    string `db:"email"`
    Age      int    `db:"age"`
    Phone    string `db:"phone"` // 新增字段
    Internal string `db:"-"`
}

// 自动迁移（只会添加 phone 列，不影响现有数据）
err = cm.AutoMigrateTable(db, &User{})
```

---

## JPA 风格实体继承完整指南

### 🎯 为什么需要实体继承？

在实际项目中，我们经常遇到这样的场景：

**问题：** 多个实体有相同的字段和方法，导致大量重复代码

```go
// ❌ 重复代码示例
type StrengthEntity struct {
    PlayerID int64 `db:"playerId,primary_key"`
    // ... 业务字段
}

type InventoryEntity struct {
    PlayerID int64 `db:"playerId,primary_key"`  // 重复！
    // ... 业务字段
}

type QuestEntity struct {
    PlayerID int64 `db:"playerId,primary_key"`  // 重复！
    // ... 业务字段
}
```

**解决方案：** 使用 JPA 风格的实体继承

```go
// ✅ 使用继承，减少 90% 重复代码
type BasePlayerEntity struct {
    PlayerID int64 `db:"playerId,primary_key"`
}

type StrengthEntity struct {
    BasePlayerEntity  // 自动继承 playerId
    // ... 业务字段
}

type InventoryEntity struct {
    BasePlayerEntity  // 自动继承 playerId
    // ... 业务字段
}

type QuestEntity struct {
    BasePlayerEntity  // 自动继承 playerId
    // ... 业务字段
}
```

### 📖 完整示例：多层继承

```go
// 第 1 层：基础实体（所有实体的基类）
type BaseEntity struct {
    CreatedAt time.Time `db:"created_at"`
    UpdatedAt time.Time `db:"updated_at"`
}

func (b *BaseEntity) BeforeSaveToDb() {
    now := time.Now()
    if b.CreatedAt.IsZero() {
        b.CreatedAt = now
    }
    b.UpdatedAt = now
}

// 第 2 层：玩家基础实体
type BasePlayerEntity struct {
    BaseEntity  // 继承第 1 层
    PlayerID int64 `db:"playerId,primary_key"`
}

func (b *BasePlayerEntity) GetPlayerID() int64 {
    return b.PlayerID
}

func (b *BasePlayerEntity) SetPlayerID(id int64) {
    b.PlayerID = id
}

// 第 3 层：具体业务实体
type StrengthEntity struct {
    BasePlayerEntity  // 继承第 2 层（间接继承第 1 层）
    
    // 业务字段
    CurrentStrength int   `db:"current_strength"`
    MaxStrength     int   `db:"max_strength"`
    
    // 忽略字段（不存储到数据库）
    cachedPowerLevel float64 `db:"-"`
}

// 实现 IDbEntity 接口
func (e *StrengthEntity) TableName() string {
    return "StrengthEntity"
}

func (e *StrengthEntity) SerializeBeforeSaveDb() {
    e.BeforeSaveToDb()  // 调用父类钩子
}

func (e *StrengthEntity) DeserializeAfterLoadDb() {
    // 自动计算缓存值
    e.cachedPowerLevel = float64(e.CurrentStrength) / float64(e.MaxStrength) * 100
}
```

### 🚀 使用继承后的实体

```go
// 1. 自动建表（支持嵌入结构体）
cm := db233.GetCrudManagerInstance()
cm.AutoMigrateTableSimple(db, &StrengthEntity{})

// 生成的表包含所有继承的字段：
// - playerId (来自 BasePlayerEntity)
// - created_at (来自 BaseEntity)
// - updated_at (来自 BaseEntity)
// - current_strength (自己定义)
// - max_strength (自己定义)

// 2. 创建实体
entity := &StrengthEntity{
    BasePlayerEntity: BasePlayerEntity{
        BaseEntity: BaseEntity{}, // 时间戳会自动设置
        PlayerID:   1000022,      // 主键（自动检测）
    },
    CurrentStrength: 100,
    MaxStrength:     500,
}

// 3. 使用继承的方法
playerID := entity.GetPlayerID()  // 来自 BasePlayerEntity
entity.SetPlayerID(1000023)       // 来自 BasePlayerEntity

// 4. 保存（UPSERT，自动处理主键冲突）
repo := db233.NewBaseCrudRepository(db)
repo.Save(entity)  // 第一次：INSERT

// 5. 更新（不会报错）
entity.CurrentStrength = 200
repo.Save(entity)  // 第二次：自动变为 UPDATE

// 6. 查询
found, _ := repo.FindById(int64(1000022), &StrengthEntity{})
foundEntity := found.(*StrengthEntity)
// 自动调用 DeserializeAfterLoadDb()，计算 cachedPowerLevel
```

### ⚙️ 核心功能

| 功能 | 说明 | 代码示例 |
|------|------|---------|
| **自动主键检测** | 无需实现 `GetDbUid()` | `PlayerID int64 \`db:"playerId,primary_key"\`` |
| **字段自动继承** | 子类自动拥有父类字段 | `BasePlayerEntity` → `StrengthEntity` |
| **方法自动继承** | 子类自动拥有父类方法 | `GetPlayerID()`、`SetPlayerID()` |
| **多层继承** | 支持 3 层或更多 | `BaseEntity` → `BasePlayerEntity` → `StrengthEntity` |
| **字段忽略** | 两种方式忽略字段 | `db:"-"` 或无 `db` tag |
| **UPSERT 处理** | 自动避免主键冲突 | INSERT...ON DUPLICATE KEY UPDATE |
| **钩子方法** | 保存前/加载后回调 | `BeforeSaveToDb()`、`AfterLoadFromDb()` |
| **线程安全** | 并发安全的缓存 | 内置 RWMutex 保护 |

### 📊 性能对比

| 项目 | 手动实现 | 自动检测 | 提升 |
|------|---------|---------|------|
| 代码行数 | 10+ 行/实体 | 0 行 | **减少 100%** |
| 主键定义 | 手动实现方法 | 自动检测 | **省时 90%** |
| 错误风险 | 容易拼写错误 | 编译时检查 | **更安全** |
| 维护成本 | 每个实体单独修改 | 修改父类即可 | **更易维护** |

### 🔗 详细文档

- 📘 [JPA 继承功能说明（中文）](docs/JPA_INHERITANCE_CN.md) - 完整的中文教程
- 📗 [JPA Inheritance Guide (English)](docs/JPA_INHERITANCE_GUIDE.md) - Complete English guide
- 📙 [快速参考卡片](docs/QUICK_REFERENCE.md) - 语法速查
- 💻 [完整示例代码](examples/player_entity_example.go) - 可运行的示例

---

### 5. 使用事务管理

```go
// 编程式事务
tm := db233.NewTransactionManager(db)
err := tm.ExecuteInTransaction(func(tm *db233.TransactionManager) error {
    // 在事务中执行操作
    _, err := tm.Exec("UPDATE users SET age = age + 1 WHERE id = ?", 1)
    if err != nil {
        return err
    }

    // 创建保存点
    err = tm.Savepoint("update_age")
    if err != nil {
        return err
    }

    // 更多操作...
    return nil
})

// 声明式事务
err = db233.WithTransaction(db, func(tm *db233.TransactionManager) error {
    // 事务操作
    return nil
}, db233.TransactionOptions{
    Isolation: sql.LevelReadCommitted,
    ReadOnly:  false,
})
```

### 6. 使用数据迁移

```go
// 创建迁移管理器
mm := db233.NewMigrationManager(db, "./migrations")

// 初始化迁移表
err := mm.Init()
if err != nil {
    panic(err)
}

// 创建新迁移
err = mm.CreateMigration("add_user_table")
if err != nil {
    panic(err)
}

// 执行上迁
err = mm.Up(0) // 0 表示应用所有待迁移
if err != nil {
    panic(err)
}

// 查看迁移状态
migrations, err := mm.GetStatus()
if err != nil {
    panic(err)
}

for _, m := range migrations {
    fmt.Printf("Migration: %d_%s, Applied: %v\n", m.Version, m.Name, m.AppliedAt != nil)
}
```

### 7. 使用健康检查

```go
// 创建健康检查器
hc := db233.NewHealthChecker(db)

// 执行健康检查
result := hc.Check()
if result.Healthy {
    fmt.Printf("数据库健康: %s\n", result.Message)
} else {
    fmt.Printf("数据库不健康: %s\n", result.Message)
}

// 定期健康检查
scheduler := db233.NewHealthCheckScheduler(30 * time.Second)
scheduler.AddChecker("main_db", hc)
scheduler.Start()

// 稍后停止
defer scheduler.Stop()
```

### 8. 使用配置管理

```go
// 从文件加载配置
cm := db233.GetConfigManager()
err := cm.LoadFromFile("config.json")
if err != nil {
    panic(err)
}

// 获取配置值
dbHost := db233.GetConfigString("database.host", "localhost")
dbPort := db233.GetConfigInt("database.port", 3306)

// 从环境变量加载
cm.LoadFromEnv("DB233_")
```

### 9. 使用日志系统

```go
// 设置日志级别
logger := db233.GetLogger()
logger.SetLevel(db233.DEBUG)

// 记录日志
db233.LogInfo("应用启动完成")
db233.LogWarn("发现配置问题: %s", issue)
db233.LogError("数据库连接失败: %v", err)
```

### 10. 使用分片

```go
// 配置分片策略
strategy := &db233.ShardingDbStrategy100w{}

// 计算分片ID
dbId := strategy.CalculateDbId(12345) // 根据用户ID计算数据库分片
```

## 配置

### 数据库配置获取器

实现 `DbConfigFetcher` 接口来提供数据库配置：

```go
type MyDbConfigFetcher struct{}

func (f *MyDbConfigFetcher) Fetch(groupName string) ([]*db233.DbConfig, error) {
    return []*db233.DbConfig{
        {
            DbId:       0,
            Url:        "user:password@tcp(localhost:3306)/db0",
            DriverName: "mysql",
        },
        {
            DbId:       1,
            Url:        "user:password@tcp(localhost:3306)/db1",
            DriverName: "mysql",
        },
    }, nil
}
```

## 架构组件

- **DbManager**: 单例数据库管理器
- **DbGroup**: 数据库组，包含多个数据库实例
- **Db**: 单个数据库连接和操作
- **CrudRepository**: CRUD 操作接口
- **CrudManager**: 实体元数据管理
- **ShardingStrategy**: 分片策略接口
- **PluginManager**: 插件管理系统
- **EntityCacheManager**: 实体元数据缓存
- **PackageScanner**: 类型注册和扫描

## 插件系统

db233-go 提供了强大的插件系统，允许在数据库操作的关键节点插入自定义逻辑。

### 内置插件

#### 日志插件
记录所有 SQL 执行信息：

```go
loggingPlugin := db233.NewLoggingPlugin()
pluginManager := db233.GetPluginManagerInstance()
pluginManager.RegisterPlugin(loggingPlugin)
```

#### 性能监控插件
监控慢查询和性能指标：

```go
performancePlugin := db233.NewPerformanceMonitorPlugin()
performancePlugin.SetSlowQueryThreshold(100 * time.Millisecond)
pluginManager.RegisterPlugin(performancePlugin)
```

#### 指标收集插件
收集数据库操作统计信息：

```go
metricsPlugin := db233.NewMetricsPlugin()
pluginManager.RegisterPlugin(metricsPlugin)

// 获取指标数据
metrics := metricsPlugin.GetMetrics()
fmt.Printf("总查询数: %d\n", metrics["total_queries"])
fmt.Printf("总耗时: %v\n", metrics["total_duration"])

// 打印报告
metricsPlugin.PrintReport()
```

### 自定义插件

实现 `Db233Plugin` 接口创建自定义插件：

```go
type MyCustomPlugin struct {
    *db233.AbstractDb233Plugin
}

func NewMyCustomPlugin() *MyCustomPlugin {
    return &MyCustomPlugin{
        AbstractDb233Plugin: db233.NewAbstractDb233Plugin("my-plugin"),
    }
}

func (p *MyCustomPlugin) InitPlugin() {
    // 初始化逻辑
}

func (p *MyCustomPlugin) PreExecuteSql(context *db233.ExecuteSqlContext) {
    // SQL 执行前逻辑
}

func (p *MyCustomPlugin) PostExecuteSql(context *db233.ExecuteSqlContext) {
    // SQL 执行后逻辑
}

// 注册插件
pluginManager.RegisterPlugin(NewMyCustomPlugin())
```

### 插件生命周期

1. **InitPlugin()**: 插件初始化
2. **PreExecuteSql()**: SQL 执行前钩子
3. **PostExecuteSql()**: SQL 执行后钩子

所有插件都是线程安全的，支持并发操作。

### 完整示例

```go
package main

import (
    "reflect"
    "github.com/SolarisNeko/db233-go/pkg/db233"
)

// 定义实体
type User struct {
    ID   int    `db:"id,primary_key"`
    Name string `db:"name"`
    Age  int    `db:"age"`
}

type Product struct {
    ID    int     `db:"id,primary_key"`
    Name  string  `db:"name"`
    Price float64 `db:"price"`
}

// 定义仓库接口
type Repository interface {
    Save(entity interface{}) error
    FindById(id interface{}) interface{}
}

// 实现仓库
type UserRepository struct {
    db *db233.Db
}

func (r *UserRepository) Save(entity interface{}) error {
    // 实现保存逻辑
    return nil
}

func (r *UserRepository) FindById(id interface{}) interface{} {
    // 实现查找逻辑
    return nil
}

func init() {
    // 在init函数中注册类型
    scanner := db233.PackageScannerInstance
    scanner.RegisterType(reflect.TypeOf(User{}))
    scanner.RegisterType(reflect.TypeOf(Product{}))
    scanner.RegisterType(reflect.TypeOf(UserRepository{}))
}

func main() {
    // 初始化数据库管理器
    manager := db233.GetInstance()

    // 配置数据库组
    config := &db233.DbGroupConfig{
        GroupName: "app",
        DbConfigFetcher: &YourDbConfigFetcher{},
    }

    dbGroup, _ := db233.NewDbGroup(config)
    manager.AddDbGroup(dbGroup)

    // 使用包扫描器自动发现实体
    scanner := db233.PackageScannerInstance

    // 扫描所有实体
    entities := scanner.ScanStructTypes("main")
    for _, entityType := range entities {
        // 自动初始化实体元数据
        crudManager := db233.GetCrudManagerInstance()
        crudManager.AutoInitEntity(entityType)
    }

    // 扫描所有仓库
    repoInterface := reflect.TypeOf((*Repository)(nil)).Elem()
    repositories := scanner.ScanSubTypes("main", repoInterface)

    fmt.Printf("发现 %d 个实体和 %d 个仓库\n", len(entities), len(repositories))
}
```

## 高级监控系统

db233-go 提供了企业级的监控系统，包括性能监控、指标收集、告警管理和报告生成。所有监控组件都支持程序化访问，无需Web界面。

### 监控组件概述

- **PerformanceMonitor**: 详细的性能监控和统计
- **ConnectionPoolMonitor**: 连接池状态监控
- **HealthChecker**: 数据库健康检查
- **AlertManager**: 基于阈值的告警系统
- **MetricsCollector**: 历史指标收集和存储
- **MetricsAggregator**: 多源指标聚合
- **MonitoringDashboard**: 统一的监控仪表板
- **MonitoringReportGenerator**: 多格式报告生成

### 性能监控器

详细监控数据库操作性能：

```go
// 创建性能监控器
perfMonitor := db233.NewPerformanceMonitor("main_db", 1000)
perfMonitor.SetSlowQueryThreshold(time.Second)

// 记录查询性能
perfMonitor.RecordQuery("SELECT", 150*time.Millisecond, true)

// 获取详细报告
report := perfMonitor.GetDetailedReport()
fmt.Printf("总查询数: %d\n", report["total_queries"])
fmt.Printf("成功率: %.2f%%\n", report["success_rate"].(float64)*100)
fmt.Printf("平均响应时间: %s\n", report["avg_query_time"])
```

### 连接池监控器

监控连接池状态和利用率：

```go
// 创建连接池监控器
connMonitor := db233.NewConnectionPoolMonitor("main_db", dataSource)

// 获取连接池报告
report := connMonitor.GetReport()
fmt.Printf("活跃连接: %d\n", report["active_connections"])
fmt.Printf("空闲连接: %d\n", report["idle_connections"])
fmt.Printf("连接利用率: %.2f%%\n", report["connection_utilization"].(float64)*100)
```

### 健康检查器

全面的数据库健康检查：

```go
// 创建健康检查器
healthChecker := db233.NewHealthChecker("main_db", dataSource)

// 添加检查项
healthChecker.AddCheck("connectivity", db233.HealthCheckConnectivity)
healthChecker.AddCheck("query_test", db233.HealthCheckQueryTest)

// 执行检查
result := healthChecker.Check()
fmt.Printf("健康状态: %t\n", result.Healthy)
fmt.Printf("响应时间: %v\n", result.ResponseTime)
```

### 告警管理器

基于阈值的智能告警：

```go
// 创建告警管理器
alertManager := db233.NewAlertManager("main_db")

// 添加告警规则
alertManager.AddRule(&db233.AlertRule{
    Name:        "high_error_rate",
    Description: "错误率过高",
    Severity:    db233.Warning,
    Condition: func(metrics map[string]interface{}) bool {
        if errorRate, ok := metrics["error_rate"].(float64); ok {
            return errorRate > 0.1 // 10%
        }
        return false
    },
    Cooldown: time.Minute * 5,
})

// 检查规则并触发告警
alertManager.CheckRules(map[string]interface{}{
    "error_rate": 0.15,
})

// 获取活跃告警
activeAlerts := alertManager.GetActiveAlerts()
for _, alert := range activeAlerts {
    fmt.Printf("告警: %s (%s)\n", alert.Name, alert.Severity)
}
```

### 指标收集器

历史指标收集和趋势分析：

```go
// 创建指标收集器 (30天保留期)
collector := db233.NewMetricsCollector("main_db", 30)

// 收集指标
collector.CollectMetric("query_duration", 150.5)
collector.CollectMetric("connection_count", 25.0)

// 获取指标历史
history := collector.GetMetricHistory("query_duration", 24*time.Hour)
fmt.Printf("收集了 %d 个数据点\n", len(history))

// 导出数据
collector.ExportData("metrics_export.json")
```

### 指标聚合器

多源指标聚合和统计：

```go
// 创建指标聚合器
aggregator := db233.NewMetricsAggregator("main_db")

// 添加数据源
aggregator.AddDataSource(perfMonitor)
aggregator.AddDataSource(connMonitor)
aggregator.AddDataSource(healthChecker)

// 刷新聚合数据
aggregator.RefreshMetrics()

// 获取聚合统计
stats := aggregator.GetAggregatedStats()
fmt.Printf("总指标数: %d\n", stats.TotalMetrics)
fmt.Printf("平均值: %.2f\n", stats.AverageValue)
fmt.Printf("最大值: %.2f\n", stats.MaxValue)
```

### 监控仪表板

统一的监控数据展示：

```go
// 创建监控仪表板
dashboard := db233.NewMonitoringDashboard("main_dashboard")

// 添加监控组件
dashboard.AddPerformanceMonitor("main_db", perfMonitor)
dashboard.AddConnectionMonitor("main_db", connMonitor)
dashboard.AddHealthChecker("main_db", healthChecker)
dashboard.AddAlertManager("main_db", alertManager)

// 启动自动刷新
dashboard.SetRefreshInterval(30 * time.Second)
dashboard.EnableAutoRefresh()
dashboard.Start()

// 获取当前快照
snapshot := dashboard.GetCurrentSnapshot()
fmt.Printf("数据库总数: %d\n", snapshot.Summary.TotalDatabases)
fmt.Printf("健康数据库: %d\n", snapshot.Summary.HealthyDatabases)
fmt.Printf("活跃告警: %d\n", snapshot.Summary.ActiveAlerts)
```

### 监控报告生成

生成多格式监控报告：

```go
// 创建报告生成器
reportGenerator := db233.NewMonitoringReportGenerator("main_reports")

// 添加监控组件
reportGenerator.AddPerformanceMonitor("main_db", perfMonitor)
reportGenerator.AddConnectionMonitor("main_db", connMonitor)
reportGenerator.AddHealthChecker("main_db", healthChecker)

// 生成并导出报告
reportGenerator.ExportReport("daily_report", "json")  // JSON格式
reportGenerator.ExportReport("daily_report", "text")  // 文本格式
reportGenerator.ExportReport("daily_report", "html")  // HTML格式
```

### 完整监控系统示例

```go
package main

import (
    "fmt"
    "time"
    "github.com/SolarisNeko/db233-go/pkg/db233"
)

func main() {
    // 初始化数据库管理器
    dbManager := db233.NewDbManager("example_db")

    // 配置数据库连接
    config := &db233.DbConfig{
        Host: "localhost", Port: 3306,
        Database: "test_db", Username: "root", Password: "password",
        MaxOpenConns: 10, MaxIdleConns: 5,
    }
    dbManager.AddDataSource("main_db", config)

    // 创建监控组件
    perfMonitor := db233.NewPerformanceMonitor("main_db", 1000)
    connMonitor := db233.NewConnectionPoolMonitor("main_db", dbManager.GetDataSource("main_db"))
    healthChecker := db233.NewHealthChecker("main_db", dbManager.GetDataSource("main_db"))
    alertManager := db233.NewAlertManager("main_db")
    metricsCollector := db233.NewMetricsCollector("main_db", 30)
    metricsAggregator := db233.NewMetricsAggregator("main_db")

    // 创建监控仪表板
    dashboard := db233.NewMonitoringDashboard("main_dashboard")
    dashboard.AddPerformanceMonitor("main_db", perfMonitor)
    dashboard.AddConnectionMonitor("main_db", connMonitor)
    dashboard.AddHealthChecker("main_db", healthChecker)
    dashboard.AddAlertManager("main_db", alertManager)
    dashboard.AddMetricsCollector("main_db", metricsCollector)
    dashboard.AddMetricsAggregator("main_db", metricsAggregator)

    // 启动监控系统
    dashboard.Start()

    // 模拟数据库操作
    for i := 0; i < 100; i++ {
        start := time.Now()
        _, err := dbManager.GetDataSource("main_db").Query("SELECT 1")
        duration := time.Since(start)

        perfMonitor.RecordQuery("SELECT", duration, err == nil)
        metricsCollector.CollectMetric("query_duration", float64(duration.Milliseconds()))
    }

    // 检查监控数据
    snapshot := dashboard.GetCurrentSnapshot()
    fmt.Printf("监控摘要:\n")
    fmt.Printf("  数据库总数: %d\n", snapshot.Summary.TotalDatabases)
    fmt.Printf("  健康数据库: %d\n", snapshot.Summary.HealthyDatabases)
    fmt.Printf("  总查询数: %d\n", snapshot.Summary.TotalQueries)
    fmt.Printf("  活跃告警: %d\n", snapshot.Summary.ActiveAlerts)

    // 生成报告
    dashboard.GenerateReport("monitoring_report", "json")
    dashboard.GenerateReport("monitoring_report", "html")

    // 清理资源
    dashboard.Stop()
    metricsCollector.Stop()
}
```

### 监控最佳实践

1. **定期检查**: 设置自动刷新间隔，定期检查系统状态
2. **阈值告警**: 为关键指标设置合理的告警阈值
3. **历史数据**: 保留足够的历史数据用于趋势分析
4. **报告生成**: 定期生成报告用于审计和优化
5. **资源清理**: 及时清理过期数据和停止监控组件

### 监控指标说明

- **性能指标**: 查询响应时间、成功率、慢查询率、QPS
- **连接指标**: 活跃连接数、空闲连接数、利用率、等待连接数
- **健康指标**: 连接状态、响应时间、检查通过率
- **告警指标**: 活跃告警数、告警严重程度分布
- **系统指标**: CPU使用率、内存使用率、磁盘I/O

---

## 📦 发布流程

### 自动发布（推荐）

使用自动化脚本进行发布，会自动读取 `version.txt` 并自增版本号：

**PowerShell:**
```powershell
# Patch 版本自增 (0.0.9 -> 0.0.10)
.\publish.ps1

# Minor 版本自增 (0.0.9 -> 0.1.0)
.\publish.ps1 -VersionPart minor

# Major 版本自增 (0.0.9 -> 1.0.0)
.\publish.ps1 -VersionPart major

# 模拟运行（不实际提交）
.\publish.ps1 -DryRun
```

**Windows CMD:**
```cmd
publish.cmd
```

脚本会自动执行以下步骤：
1. ✅ 读取 `version.txt` 当前版本
2. ✅ 自动计算下一个版本号
3. ✅ 拉取最新代码
4. ✅ 清理并构建项目
5. ✅ **运行所有测试（必须通过）**
6. ✅ 更新 `version.txt`
7. ✅ 自动提交所有更改
8. ✅ 创建 Git Tag
9. ✅ 推送到远程仓库

### 手动发布

如果需要手动控制版本号：

1. 修改 `version.txt` 文件
2. 运行测试确保通过
3. 提交更改并创建标签
4. 推送到远程仓库

---

## 📚 示例代码说明

### ⚠️ 重要提示

`examples/` 目录中的代码**仅供参考学习使用**，类似于 JUnit 的测试代码，**不应该被外部项目直接引用**。

**正确的使用方式：**

```go
// ✅ 正确：直接导入主包
import "github.com/neko233-com/db233-go/pkg/db233"

// ❌ 错误：不要导入 examples
// import "github.com/neko233-com/db233-go/examples"
```

### 示例代码位置

- **完整示例：** [examples/player_entity_example.go](examples/player_entity_example.go)
  - 多层继承示例
  - JPA 风格实体定义
  - CRUD 操作演示

- **单元测试：** [tests/embedded_struct_test.go](tests/embedded_struct_test.go)
  - 嵌入结构体测试
  - 主键检测测试
  - UPSERT 功能测试

### 如何学习

1. **查看示例代码** - 了解如何使用各种功能
2. **运行示例** - 在本地克隆仓库后运行示例
3. **复制代码** - 将示例代码复制到你的项目中并修改
4. **阅读文档** - 参考详细文档了解更多

**本地运行示例：**

```bash
# 克隆仓库
git clone https://github.com/neko233-com/db233-go.git
cd db233-go

# 查看示例代码
cat examples/player_entity_example.go

# 运行测试（包含示例）
go test ./tests -v
```

---

## ❓ 常见问题与故障排除

### 问题 1: "Field 'xxx' doesn't have a default value" 错误

**错误信息：**
```
Error 1364 (HY000): Field 'rankId' doesn't have a default value
```

**原因：**
主键字段的值为零值（int 类型为 0，string 类型为 ""），被自动跳过，未包含在 INSERT 语句中。

**解决方案：**

1. **为主键赋非零值（手动设置 ID）：**
```go
// ✅ 正确
entity := &RankEntity{
    RankId: 1001,  // 非零值，会被包含在 INSERT 中
    RankName: "test",
}
```

2. **使用自增主键（让数据库生成 ID）：**
```go
type RankEntity struct {
    RankId int `db:"rankId,primary_key,auto_increment"` // 添加 auto_increment
    // ...其他字段
}

// 保存时不需要设置 RankId，数据库会自动生成
entity := &RankEntity{
    RankName: "test",
}
```

3. **使用指针类型区分零值和未设置：**
```go
type RankEntity struct {
    RankId *int `db:"rankId,primary_key"` // 使用指针
    // ...其他字段
}

// nil 表示未设置，0 表示真的想设置为 0
rankId := 1001
entity := &RankEntity{
    RankId: &rankId,
    RankName: "test",
}
```

### 问题 2: UPSERT 行为说明

**问题：** 为什么 Save 会自动变成 UPDATE？

**说明：**
db233-go 默认使用 `INSERT ... ON DUPLICATE KEY UPDATE` 语法（UPSERT），自动处理主键冲突：

```go
// 第一次保存 - 执行 INSERT
user := &User{ID: 1000022, Username: "john"}
repo.Save(user) // INSERT

// 第二次保存相同主键 - 自动变为 UPDATE
user.Username = "john_updated"
repo.Save(user) // UPDATE（不会报错）
```

**优点：**
- ✅ 避免主键冲突错误
- ✅ 减少业务代码复杂度
- ✅ 自动判断 INSERT 还是 UPDATE

### 问题 3: 嵌入结构体的字段未被识别

**问题：** 继承的字段没有保存到数据库

**检查清单：**

1. ✅ 嵌入字段是否有 `db` 标签？
```go
type BaseEntity struct {
    PlayerID int64 `db:"playerId,primary_key"` // 必须有 db 标签
}
```

2. ✅ 嵌入方式是否正确？
```go
type StrengthEntity struct {
    BaseEntity  // ✅ 正确：匿名嵌入
    // ...
}

// 而不是：
type StrengthEntity struct {
    Base BaseEntity  // ❌ 错误：命名字段不会被递归扫描
}
```

3. ✅ 是否调用了 `AutoInitEntity` 或 `AutoMigrateTable`？
```go
cm := db233.GetCrudManagerInstance()
cm.AutoInitEntity(&StrengthEntity{}) // 必须初始化
```

### 问题 4: 字段被意外跳过

**问题：** 某些字段没有保存到数据库

**检查项：**

1. 是否有 `db` 标签？
```go
Name string `db:"name"` // ✅ 有标签，会保存
Age  int    // ❌ 无标签，会被跳过
```

2. 是否标记为跳过？
```go
Internal string `db:"-"` // ✅ 明确跳过
Temp     string `db:"temp,skip"` // ✅ 明确跳过
```

3. 字段是否为未导出字段？
```go
Name string `db:"name"` // ✅ 导出字段（首字母大写）
age  int    `db:"age"`  // ❌ 未导出字段，无法访问
```

---

## 🤝 贡献指南

欢迎贡献代码！请遵循以下步骤：

1. Fork 本仓库
2. 创建特性分支 (`git checkout -b feature/AmazingFeature`)
3. 提交更改 (`git commit -m 'Add some AmazingFeature'`)
4. 推送到分支 (`git push origin feature/AmazingFeature`)
5. 创建 Pull Request

**贡献规范：**

- 代码必须通过所有测试
- 添加必要的单元测试
- 更新相关文档
- 遵循 Go 代码规范

---

## 📄 许可证

本项目采用与原 Kotlin 版本相同的许可证。

---

## 🔗 相关链接

- **GitHub 仓库：** https://github.com/neko233-com/db233-go
- **问题反馈：** https://github.com/neko233-com/db233-go/issues
- **原 Kotlin 版本：** https://github.com/neko233-com/db233

---

**最后更新：** 2026-01-10  
**当前版本：** v0.0.9  
**作者：** neko233

