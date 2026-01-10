# DB233-Go JPA 继承快速参考

## 基本语法

### 父类定义
```go
type BasePlayerEntity struct {
    PlayerID int64 `db:"playerId,primary_key"`
}
```

### 子类继承
```go
type StrengthEntity struct {
    BasePlayerEntity  // 嵌入 = 继承
    CurrentStrength int `db:"current_strength"`
}
```

## 接口实现

```go
func (e *StrengthEntity) TableName() string {
    return "StrengthEntity"
}

func (e *StrengthEntity) SerializeBeforeSaveDb()   {}
func (e *StrengthEntity) DeserializeAfterLoadDb() {}
```

## 字段标签

| 标签 | 说明 |
|------|------|
| `db:"name"` | 列名 |
| `db:"name,primary_key"` | 主键 |
| `db:"name,auto_increment"` | 自增 |
| `db:"-"` | 忽略 |
| 无 `db` | 忽略 |

## CRUD 操作

```go
// 自动建表
cm := db233.GetCrudManagerInstance()
cm.AutoMigrateTableSimple(db, &StrengthEntity{})

// CRUD
repo := db233.NewBaseCrudRepository(db)
repo.Save(entity)    // UPSERT
repo.FindById(id, &StrengthEntity{})
repo.Update(entity)
repo.DeleteById(id, &StrengthEntity{})
```

## 多层继承

```go
type BaseEntity struct {
    CreatedAt time.Time `db:"created_at"`
}

type BasePlayerEntity struct {
    BaseEntity
    PlayerID int64 `db:"playerId,primary_key"`
}

type StrengthEntity struct {
    BasePlayerEntity
    CurrentStrength int `db:"current_strength"`
}
```

## 常见错误

❌ 字段小写：`id int64`  
✅ 字段大写：`ID int64`

❌ 无 db 标签：`Name string`  
✅ 有 db 标签：`Name string \`db:"name"\``

## 优势

✅ 自动主键检测  
✅ UPSERT 自动处理  
✅ 减少重复代码  
✅ 方法自动继承  
✅ 线程安全

