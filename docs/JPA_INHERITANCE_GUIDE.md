# DB233 - JPA 风格的实体继承支持文档

> 类似 Java JPA 的 `@Entity`、`@Id`、`@Column` 机制，支持 Go 结构体嵌入（Embedded Struct）实现继承

## 📋 目录

- [功能概述](#功能概述)
- [核心特性](#核心特性)
- [快速开始](#快速开始)
- [详细说明](#详细说明)
- [最佳实践](#最佳实践)
- [常见问题](#常见问题)

---

## 功能概述

DB233 通过 Go 的**结构体嵌入（Embedded Struct）**机制，实现了类似 Java JPA 的实体继承功能。子类可以自动继承父类的：

- ✅ **主键字段** (`@Id` 等价于 `db:"xxx,primary_key"`)
- ✅ **普通列字段** (`@Column` 等价于 `db:"column_name"`)
- ✅ **回调方法** (如 `BeforeSaveToDb()`、`AfterLoadFromDb()`)
- ✅ **业务方法** (如 `GetPlayerID()`、`SetPlayerID()`)

---

## 核心特性

### 1. 自动主键检测 🔑

**无需手动实现 `GetDbUid()` 方法**，框架自动从结构体 tag 中检测主键。

#### Java JPA 写法：
```java
@Entity
public abstract class BasePlayerEntity {
    @Id
    @Column(name = "playerId")
    private Long playerId;
}

@Entity
public class StrengthEntity extends BasePlayerEntity {
    @Column(name = "current_strength")
    private Integer currentStrength;
}
```

#### DB233 (Go) 等价写法：
```go
// 基础实体（父类）
type BasePlayerEntity struct {
    // 主键：db:"playerId,primary_key" 相当于 JPA 的 @Id
    PlayerID int64 `json:"playerId" db:"playerId,primary_key"`
}

// 业务实体（子类）
type StrengthEntity struct {
    BasePlayerEntity  // 嵌入父类，自动继承 playerId 主键
    CurrentStrength int `json:"currentStrength" db:"current_strength"`
}
```

**效果：**
- ✅ 自动识别 `playerId` 为主键
- ✅ 自动生成包含 `playerId` 的 CREATE TABLE 语句
- ✅ 自动在 INSERT/UPDATE 时处理 `playerId`

---

### 2. 字段忽略机制 🚫

支持两种方式忽略字段，类似 JPA 的 `@Transient`：

| 方式 | 说明 | JPA 等价 |
|------|------|---------|
| `db:"-"` | 明确标记忽略 | `@Transient` |
| 无 `db` tag | 默认忽略 | 不加 `@Column` |

```go
type MyEntity struct {
    ID          int64  `db:"id,primary_key"`
    Name        string `db:"name"`              // ✅ 会存储
    TempField   string `db:"-"`                 // ❌ 不存储（显式忽略）
    CacheValue  string                          // ❌ 不存储（无 db tag）
}
```

---

### 3. UPSERT 自动处理 🔄

**所有 `Save()` 操作自动使用 UPSERT 逻辑**，避免主键冲突错误。

```go
entity := &StrengthEntity{
    BasePlayerEntity: BasePlayerEntity{PlayerID: 1000022},
    CurrentStrength:  100,
}

// 第一次：INSERT（主键不存在）
repo.Save(entity) // ✅ 插入成功

// 第二次：UPDATE（主键已存在）
entity.CurrentStrength = 200
repo.Save(entity) // ✅ 自动更新，不报错！
```

**底层 SQL：**
```sql
INSERT INTO StrengthEntity (playerId, current_strength) 
VALUES (1000022, 200) 
ON DUPLICATE KEY UPDATE current_strength = VALUES(current_strength);
```

---

### 4. 多层继承支持 🏗️

支持多层嵌套继承（类似 Java 的多层继承）：

```go
// 第一层：基础实体
type BaseEntity struct {
    ID int64 `db:"id,primary_key,auto_increment"`
    CreatedAt time.Time `db:"created_at"`
}

// 第二层：玩家基础实体
type BasePlayerEntity struct {
    BaseEntity  // 继承 id 和 created_at
    PlayerID int64 `db:"player_id"`
}

// 第三层：具体业务实体
type StrengthEntity struct {
    BasePlayerEntity  // 继承所有父类字段
    CurrentStrength int `db:"current_strength"`
}
```

**效果：** StrengthEntity 自动拥有 `id`、`created_at`、`player_id`、`current_strength` 四个字段。

---

## 快速开始

### 步骤 1：定义基础实体类

```go
package player

import (
    db233 "github.com/neko233-com/db233-go/pkg/db233"
)

// BasePlayerEntity 基础玩家实体（类似 JPA 的抽象基类）
type BasePlayerEntity struct {
    // 主键：必须标记 primary_key
    PlayerID int64 `json:"playerId" db:"playerId,primary_key"`
}

// ========== 业务方法（子类自动继承） ==========

func (b *BasePlayerEntity) GetPlayerID() int64 {
    return b.PlayerID
}

func (b *BasePlayerEntity) SetPlayerID(playerID int64) {
    b.PlayerID = playerID
}

// ========== 钩子方法（子类可重写） ==========

func (b *BasePlayerEntity) AfterLoadFromDb() {
    // 从数据库加载后的回调
}

func (b *BasePlayerEntity) BeforeSaveToDb() {
    // 保存到数据库前的回调
}
```

### 步骤 2：定义业务实体（子类）

```go
// StrengthEntity 力量实体（继承 BasePlayerEntity）
type StrengthEntity struct {
    BasePlayerEntity  // 嵌入父类，自动继承 playerId 主键
    
    // 业务字段
    LastUpdateTimeMs int64 `json:"lastUpdateTimeMs" db:"last_update_time_ms"`
    CurrentStrength  int   `json:"currentStrength" db:"current_strength"`
    UpdatedAtTimeMs  int64 `json:"updatedAtTimeMs" db:"updated_at_time_ms"`
    
    // 忽略字段
    CachedValue string `db:"-"` // 不存储到数据库
}

// ========== 实现 IDbEntity 接口 ==========

func (e *StrengthEntity) TableName() string {
    return "StrengthEntity"
}

func (e *StrengthEntity) SerializeBeforeSaveDb() {
    e.BeforeSaveToDb()  // 调用父类方法
}

func (e *StrengthEntity) DeserializeAfterLoadDb() {
    e.AfterLoadFromDb()  // 调用父类方法
}
```

### 步骤 3：使用 CRUD 操作

```go
func main() {
    // 1. 创建数据库连接
    db := db233.NewDb(dataSource, 0, nil)
    
    // 2. 自动创建/迁移表（支持嵌入结构体）
    cm := db233.GetCrudManagerInstance()
    cm.AutoMigrateTableSimple(db, &StrengthEntity{})
    
    // 3. 创建 Repository
    repo := db233.NewBaseCrudRepository(db)
    
    // 4. 保存实体（UPSERT）
    entity := &StrengthEntity{
        BasePlayerEntity: BasePlayerEntity{PlayerID: 1000022},
        CurrentStrength:  100,
    }
    repo.Save(entity) // 自动识别 playerId 为主键
    
    // 5. 查询实体
    found, _ := repo.FindById(int64(1000022), &StrengthEntity{})
    
    // 6. 更新实体（再次 Save 自动变为 UPDATE）
    entity.CurrentStrength = 200
    repo.Save(entity) // 不会报主键冲突错误
}
```

---

## 详细说明

### 主键检测规则

框架按以下顺序检测主键：

1. **显式标记：** `db:"column_name,primary_key"`
2. **独立标签：** `primary_key:"true"`
3. **字段名约定：** 字段名为 `ID` 或 `Id`

**优先级：** 嵌入结构体（父类）> 当前结构体

```go
type BaseEntity struct {
    ID int64 `db:"id,primary_key"` // ✅ 会被检测到
}

type MyEntity struct {
    BaseEntity  // 优先使用父类的 id 作为主键
    MyID int64 `db:"my_id,primary_key"` // ⚠️ 会被忽略
}
```

---

### 字段扫描规则

| 规则 | 说明 |
|------|------|
| 必须导出 | 字段首字母必须大写（`PlayerID`，不是 `playerId`） |
| 必须有 `db` tag | 没有 `db` tag 的字段会被忽略 |
| `db:"-"` 忽略 | 明确标记不存储到数据库 |
| 递归扫描 | 自动扫描嵌入结构体的字段 |

---

### 自动建表示例

给定以下实体：

```go
type BasePlayerEntity struct {
    PlayerID int64 `db:"playerId,primary_key"`
}

type StrengthEntity struct {
    BasePlayerEntity
    CurrentStrength int `db:"current_strength"`
}
```

**自动生成的 SQL：**

```sql
CREATE TABLE `StrengthEntity` (
    `playerId` BIGINT NOT NULL,
    `current_strength` INT NULL,
    PRIMARY KEY (`playerId`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

---

## 最佳实践

### 1. 统一的基础实体类

```go
// 推荐：所有玩家实体继承统一的基类
type BasePlayerEntity struct {
    PlayerID  int64     `db:"playerId,primary_key"`
    CreatedAt time.Time `db:"created_at"`
    UpdatedAt time.Time `db:"updated_at"`
}

// 业务实体 1
type InventoryEntity struct {
    BasePlayerEntity
    ItemID int64 `db:"item_id"`
}

// 业务实体 2
type QuestEntity struct {
    BasePlayerEntity
    QuestID int64 `db:"quest_id"`
}
```

### 2. 钩子方法的使用

```go
type BasePlayerEntity struct {
    PlayerID  int64     `db:"playerId,primary_key"`
    UpdatedAt time.Time `db:"updated_at"`
}

func (b *BasePlayerEntity) BeforeSaveToDb() {
    // 自动更新时间戳
    b.UpdatedAt = time.Now()
}

// 子类自动继承钩子逻辑
type StrengthEntity struct {
    BasePlayerEntity
    CurrentStrength int `db:"current_strength"`
}
```

### 3. 批量操作

```go
entities := []*StrengthEntity{
    {BasePlayerEntity: BasePlayerEntity{PlayerID: 1001}, CurrentStrength: 100},
    {BasePlayerEntity: BasePlayerEntity{PlayerID: 1002}, CurrentStrength: 200},
}

// 批量保存（自动 UPSERT）
for _, entity := range entities {
    repo.Save(entity)
}
```

---

## 常见问题

### Q1: 为什么我的字段没有被存储？

**A:** 检查以下几点：
1. ✅ 字段首字母是否大写（必须导出）
2. ✅ 是否有 `db:"column_name"` tag
3. ✅ 是否标记了 `db:"-"`

```go
// ❌ 错误示例
type MyEntity struct {
    id   int64  `db:"id"`           // ❌ 小写，未导出
    Name string                     // ❌ 没有 db tag
    Age  int    `db:"-"`            // ✅ 正确，显式忽略
}

// ✅ 正确示例
type MyEntity struct {
    ID   int64  `db:"id,primary_key"` // ✅
    Name string `db:"name"`           // ✅
}
```

---

### Q2: 如何处理多个主键（复合主键）？

**A:** 目前暂不支持复合主键，建议使用单一主键 + 唯一索引：

```go
type MyEntity struct {
    ID        int64  `db:"id,primary_key,auto_increment"`
    PlayerID  int64  `db:"player_id"`   // 添加唯一索引
    ItemID    int64  `db:"item_id"`     // 添加唯一索引
}

// 手动创建唯一索引：
// ALTER TABLE MyEntity ADD UNIQUE KEY `uk_player_item` (`player_id`, `item_id`);
```

---

### Q3: 主键冲突错误怎么办？

**A:** 框架默认使用 UPSERT，不应该出现主键冲突错误。如果还是报错，检查：

1. **是否正确标记主键：** 确保 `db:"xxx,primary_key"`
2. **是否清理缓存：** 测试时调用 `cm.ClearPrimaryKeyCache()`
3. **是否表结构不一致：** 重新执行 `AutoMigrateTableSimple()`

---

### Q4: 嵌入多个结构体时，主键如何选择？

**A:** 框架按遍历顺序，**第一个找到的主键生效**：

```go
type Base1 struct {
    ID1 int64 `db:"id1,primary_key"`
}

type Base2 struct {
    ID2 int64 `db:"id2,primary_key"`
}

type MyEntity struct {
    Base1  // ✅ id1 会被选为主键
    Base2  // ❌ id2 被忽略
}
```

**建议：** 避免多个嵌入结构体都定义主键。

---

### Q5: 如何兼容 Kotlin JPA 生成的表？

**A:** 使用小驼峰命名的列名：

```go
// Kotlin JPA 生成的表：playerId（小驼峰）
type BasePlayerEntity struct {
    PlayerID int64 `db:"playerId,primary_key"` // 注意是 playerId，不是 player_id
}
```

---

## 性能优化

### 1. 缓存机制

框架自动缓存：
- ✅ 主键列名
- ✅ 所有列名
- ✅ 实体元数据

**无需手动管理缓存**，框架保证线程安全。

### 2. 批量操作

推荐使用 `SaveBatch()` 代替循环 `Save()`：

```go
// ❌ 低效写法
for _, entity := range entities {
    repo.Save(entity)
}

// ✅ 推荐写法（待实现批量 UPSERT）
repo.SaveBatch(entities)
```

---

## 兼容性

| 数据库 | 支持状态 | 说明 |
|--------|---------|------|
| MySQL | ✅ 完全支持 | 使用 `ON DUPLICATE KEY UPDATE` |
| PostgreSQL | 🚧 计划支持 | 使用 `ON CONFLICT DO UPDATE` |
| SQLite | 🚧 计划支持 | 使用 `ON CONFLICT REPLACE` |

---

## 总结

DB233 的实体继承机制让 Go 开发者能够像使用 Java JPA 一样，通过结构体嵌入实现：

1. ✅ **自动主键检测** - 无需手动实现 `GetDbUid()`
2. ✅ **字段自动继承** - 父类的列自动被子类继承
3. ✅ **方法自动继承** - 业务方法和钩子方法自动继承
4. ✅ **UPSERT 自动处理** - 避免主键冲突错误
5. ✅ **多层继承支持** - 支持多层嵌套结构体

**减少模板代码，提高开发效率！** 🚀

---

## 示例代码

完整示例请参考：
- `tests/embedded_struct_test.go` - 单元测试
- `examples/player_entity_example.go` - 完整示例（待创建）

---

**作者：** neko233  
**更新时间：** 2026-01-10  
**版本：** v1.0.0

