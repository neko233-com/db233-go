# DB233-Go JPA 实体继承功能说明（中文版）

## 📚 功能简介

DB233-Go 实现了类似 Java JPA 的实体继承机制，通过 Go 的**结构体嵌入（Embedded Struct）**，让你可以像写 Java 代码一样定义实体类的继承关系。

### 核心价值

✅ **减少重复代码** - 通用字段只需在父类定义一次  
✅ **自动主键检测** - 无需手动实现 `GetDbUid()` 方法  
✅ **方法自动继承** - 父类的业务方法子类自动拥有  
✅ **UPSERT 自动处理** - 避免主键冲突错误  
✅ **线程安全** - 元数据缓存支持并发访问  

---

## 🎯 快速对比

### Java JPA 写法

```java
@MappedSuperclass
public abstract class BasePlayerEntity {
    @Id
    @Column(name = "playerId")
    private Long playerId;
    
    public Long getPlayerId() {
        return playerId;
    }
}

@Entity
@Table(name = "StrengthEntity")
public class StrengthEntity extends BasePlayerEntity {
    @Column(name = "current_strength")
    private Integer currentStrength;
}
```

### DB233-Go 等价写法

```go
// 父类
type BasePlayerEntity struct {
    PlayerID int64 `db:"playerId,primary_key"`
}

func (b *BasePlayerEntity) GetPlayerID() int64 {
    return b.PlayerID
}

// 子类 - 嵌入父类即可
type StrengthEntity struct {
    BasePlayerEntity  // 自动继承 playerId 和 GetPlayerID()
    CurrentStrength int `db:"current_strength"`
}

func (e *StrengthEntity) TableName() string {
    return "StrengthEntity"
}

func (e *StrengthEntity) SerializeBeforeSaveDb()   {}
func (e *StrengthEntity) DeserializeAfterLoadDb() {}
```

---

## 🔧 使用方法

### 1. 定义基础实体类（父类）

```go
package player

// BasePlayerEntity 所有玩家实体的基类
type BasePlayerEntity struct {
    // 主键字段：使用 primary_key 标签
    PlayerID int64 `json:"playerId" db:"playerId,primary_key"`
}

// 业务方法（子类自动继承）
func (b *BasePlayerEntity) GetPlayerID() int64 {
    return b.PlayerID
}

func (b *BasePlayerEntity) SetPlayerID(id int64) {
    b.PlayerID = id
}

// 钩子方法（子类可重写）
func (b *BasePlayerEntity) BeforeSaveToDb() {
    // 保存前的处理逻辑
}

func (b *BasePlayerEntity) AfterLoadFromDb() {
    // 加载后的处理逻辑
}
```

### 2. 定义业务实体类（子类）

```go
// StrengthEntity 力量系统实体
type StrengthEntity struct {
    BasePlayerEntity  // 嵌入父类（相当于 Java 的 extends）
    
    // 业务字段
    CurrentStrength  int   `db:"current_strength"`
    MaxStrength      int   `db:"max_strength"`
    LastUpdateTimeMs int64 `db:"last_update_time_ms"`
    
    // 忽略字段（不存储到数据库）
    cachedValue string `db:"-"`   // 显式忽略
    tempFlag    bool              // 无 db tag，自动忽略
}

// 必须实现 IDbEntity 接口
func (e *StrengthEntity) TableName() string {
    return "StrengthEntity"
}

func (e *StrengthEntity) SerializeBeforeSaveDb() {
    e.BeforeSaveToDb()  // 调用父类钩子
}

func (e *StrengthEntity) DeserializeAfterLoadDb() {
    e.AfterLoadFromDb()  // 调用父类钩子
}
```

### 3. CRUD 操作

```go
func main() {
    // 创建数据库连接
    db := db233.NewDb(dataSource, 0, nil)
    
    // 自动创建表（支持嵌入结构体）
    cm := db233.GetCrudManagerInstance()
    cm.AutoMigrateTableSimple(db, &StrengthEntity{})
    
    // 创建 Repository
    repo := db233.NewBaseCrudRepository(db)
    
    // 创建实体
    entity := &StrengthEntity{
        BasePlayerEntity: BasePlayerEntity{
            PlayerID: 1000022,  // 主键（自动检测）
        },
        CurrentStrength: 100,
        MaxStrength:     500,
    }
    
    // 第一次保存（INSERT）
    repo.Save(entity)
    
    // 修改后再次保存（自动变为 UPDATE，不会报错！）
    entity.CurrentStrength = 200
    repo.Save(entity)  // 使用 INSERT...ON DUPLICATE KEY UPDATE
    
    // 查询
    found, _ := repo.FindById(int64(1000022), &StrengthEntity{})
    
    // 使用继承的方法
    playerID := found.(*StrengthEntity).GetPlayerID()
}
```

---

## 🎨 高级特性

### 1. 多层继承

支持多层嵌套（3层或更多）：

```go
// 第 1 层：基础实体
type BaseEntity struct {
    CreatedAt time.Time `db:"created_at"`
    UpdatedAt time.Time `db:"updated_at"`
}

// 第 2 层：玩家基础实体
type BasePlayerEntity struct {
    BaseEntity  // 继承第 1 层
    PlayerID int64 `db:"playerId,primary_key"`
}

// 第 3 层：具体业务实体
type StrengthEntity struct {
    BasePlayerEntity  // 继承第 2 层（间接继承第 1 层）
    CurrentStrength int `db:"current_strength"`
}

// StrengthEntity 自动拥有：
// - created_at (来自 BaseEntity)
// - updated_at (来自 BaseEntity)
// - playerId (来自 BasePlayerEntity)
// - current_strength (自己定义)
```

### 2. 字段忽略机制

两种方式忽略字段：

```go
type MyEntity struct {
    ID int64 `db:"id,primary_key"`
    
    // 方式 1：显式标记 db:"-"
    CachedValue string `db:"-"`
    
    // 方式 2：不写 db tag
    TempFlag bool
    
    // 这两个字段都不会存储到数据库
}
```

### 3. UPSERT 自动处理

所有 `Save()` 操作自动使用 UPSERT 逻辑：

```go
entity := &StrengthEntity{
    BasePlayerEntity: BasePlayerEntity{PlayerID: 1000022},
    CurrentStrength:  100,
}

// 第一次：INSERT（主键不存在）
repo.Save(entity)  // ✅ 成功

// 第二次：UPDATE（主键已存在）
entity.CurrentStrength = 200
repo.Save(entity)  // ✅ 自动变为 UPDATE，不报错
```

**底层 SQL：**
```sql
INSERT INTO StrengthEntity (playerId, current_strength) 
VALUES (1000022, 200) 
ON DUPLICATE KEY UPDATE current_strength = VALUES(current_strength);
```

### 4. 钩子方法

支持保存前/加载后的钩子：

```go
type BasePlayerEntity struct {
    PlayerID  int64     `db:"playerId,primary_key"`
    UpdatedAt time.Time `db:"updated_at"`
}

// 保存前自动调用
func (b *BasePlayerEntity) BeforeSaveToDb() {
    b.UpdatedAt = time.Now()  // 自动更新时间
}

// 加载后自动调用
func (b *BasePlayerEntity) AfterLoadFromDb() {
    // 数据验证或转换
}

// 子类自动继承这些钩子
type StrengthEntity struct {
    BasePlayerEntity
    CurrentStrength int `db:"current_strength"`
}
```

---

## 📋 标签说明

### db 标签格式

```go
`db:"列名,选项1,选项2,..."`
```

### 支持的选项

| 选项 | 说明 | 示例 |
|------|------|------|
| `primary_key` | 主键 | `db:"id,primary_key"` |
| `auto_increment` | 自增 | `db:"id,primary_key,auto_increment"` |
| `not_null` | 非空 | `db:"name,not_null"` |
| `-` | 忽略字段 | `db:"-"` |

### 示例

```go
type User struct {
    ID       int64  `db:"id,primary_key,auto_increment"` // 自增主键
    Username string `db:"username,not_null"`             // 非空
    Email    string `db:"email"`                         // 普通列
    Password string `db:"-"`                             // 不存储
    TempData string                                      // 无 tag，忽略
}
```

---

## ⚠️ 注意事项

### 1. 字段必须导出

```go
// ❌ 错误：字段名小写
type MyEntity struct {
    id int64 `db:"id"`
}

// ✅ 正确：字段名大写
type MyEntity struct {
    ID int64 `db:"id"`
}
```

### 2. 必须有 db 标签

```go
// ❌ 这个字段会被忽略
type MyEntity struct {
    Name string  // 没有 db 标签
}

// ✅ 正确写法
type MyEntity struct {
    Name string `db:"name"`
}
```

### 3. 主键检测规则

框架按以下顺序检测主键：

1. `db:"xxx,primary_key"` （优先）
2. `primary_key:"true"`
3. 字段名为 `ID` 或 `Id`（约定）

在嵌入结构体中，优先使用父类的主键。

### 4. 避免多个主键

```go
type Base1 struct {
    ID1 int64 `db:"id1,primary_key"`
}

type Base2 struct {
    ID2 int64 `db:"id2,primary_key"`
}

// ❌ 不推荐：两个父类都有主键
type MyEntity struct {
    Base1  // id1 会被选为主键
    Base2  // id2 会被忽略
}
```

---

## 🔍 常见问题

### Q: 为什么字段没有存储到数据库？

**A:** 检查以下几点：
1. 字段是否导出（首字母大写）
2. 是否有 `db` 标签
3. 是否标记了 `db:"-"`

### Q: 如何兼容 Kotlin JPA 生成的表？

**A:** 使用小驼峰命名：

```go
type BasePlayerEntity struct {
    // 注意：playerId 不是 player_id
    PlayerID int64 `db:"playerId,primary_key"`
}
```

### Q: 支持复合主键吗？

**A:** 目前不支持，建议使用单一主键 + 唯一索引。

### Q: 如何清理缓存？

**A:** 测试时可以调用：

```go
cm := db233.GetCrudManagerInstance()
cm.ClearPrimaryKeyCache()
```

---

## 📖 完整示例

请参考：
- `tests/embedded_struct_test.go` - 单元测试
- `examples/player_entity_example.go` - 完整示例
- `docs/JPA_INHERITANCE_GUIDE.md` - 详细指南

---

## 🚀 总结

DB233-Go 的 JPA 风格实体继承功能让你可以：

1. ✅ **像写 Java JPA 一样定义实体** - 通过结构体嵌入实现继承
2. ✅ **自动主键检测** - 无需手动实现 `GetDbUid()`
3. ✅ **减少重复代码** - 通用字段和方法只需定义一次
4. ✅ **UPSERT 自动处理** - 避免主键冲突
5. ✅ **线程安全** - 支持高并发场景

**让 Go 开发体验更接近 Java JPA！** 🎉

---

**更新时间：** 2026-01-10  
**作者：** neko233

