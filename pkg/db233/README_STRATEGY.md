# 数据库建表策略使用指南

## 概述

db233-go 现在支持多种数据库类型的自动建表功能，通过策略工厂模式实现。默认支持 MySQL 和 PostgreSQL。

## 数据库类型

```go
type DatabaseType string

const (
    DatabaseTypeMySQL      DatabaseType = "mysql"
    DatabaseTypePostgreSQL DatabaseType = "postgresql"
)
```

## 使用方式

### 1. 创建数据库连接时指定类型

#### 默认方式（MySQL）
```go
db := db233.NewDb(dataSource, 0, nil)
// 默认使用 MySQL
```

#### 指定数据库类型
```go
// 使用 MySQL
db := db233.NewDbWithType(dataSource, 0, nil, db233.DatabaseTypeMySQL)

// 使用 PostgreSQL
db := db233.NewDbWithType(dataSource, 0, nil, db233.DatabaseTypePostgreSQL)
```

### 2. 自动建表

```go
cm := db233.GetCrudManagerInstance()
cm.AutoInitEntity(&MyEntity{})

// 自动建表（会根据 db.DatabaseType 选择对应的策略）
err := cm.AutoCreateTable(db, &MyEntity{})
```

### 3. 自定义策略

如果需要支持其他数据库类型，可以实现 `ITableCreationStrategy` 接口：

```go
type CustomStrategy struct {
    cm *CrudManager
}

func (s *CustomStrategy) GetDatabaseType() DatabaseType {
    return DatabaseType("custom")
}

func (s *CustomStrategy) GenerateCreateTableSQL(tableName string, entityType reflect.Type, uidColumn string) (string, error) {
    // 实现建表 SQL 生成逻辑
}

// ... 实现其他接口方法

// 注册自定义策略
factory := db233.GetStrategyFactoryInstance()
factory.RegisterStrategy(db233.DatabaseType("custom"), &CustomStrategy{cm: cm})
```

## 策略文件结构

- `mysql_strategy.go` - MySQL 建表策略
- `postgresql_strategy.go` - PostgreSQL 建表策略
- `strategy_factory.go` - 策略工厂
- `table_creation_strategy.go` - 策略接口定义

## 数据库差异处理

### MySQL vs PostgreSQL

| 特性 | MySQL | PostgreSQL |
|------|-------|------------|
| 字符串引号 | 反引号 `` ` `` | 双引号 `"` |
| 自增字段 | `AUTO_INCREMENT` | `SERIAL` / `BIGSERIAL` |
| 布尔类型 | `TINYINT(1)` | `BOOLEAN` |
| 整数类型 | `INT` | `INTEGER` |
| 浮点类型 | `DOUBLE` | `DOUBLE PRECISION` |
| 检查表存在 | `information_schema.tables WHERE table_schema = DATABASE()` | `information_schema.tables WHERE table_schema = current_schema()` |
| 参数占位符 | `?` | `$1, $2, ...` |

所有差异已由策略自动处理，用户无需关心。

## 示例

### MySQL 示例
```go
// 创建 MySQL 数据库连接
dataSource, _ := sql.Open("mysql", "user:password@tcp(localhost:3306)/dbname")
db := db233.NewDbWithType(dataSource, 0, nil, db233.DatabaseTypeMySQL)

// 定义实体
type User struct {
    ID   int    `db:"id,primary_key,auto_increment"`
    Name string `db:"name"`
}

func (u *User) TableName() string { return "users" }
func (u *User) GetDbUid() string { return "id" }
func (u *User) SerializeBeforeSaveDb() {}
func (u *User) DeserializeAfterLoadDb() {}

// 自动建表
cm := db233.GetCrudManagerInstance()
cm.AutoInitEntity(&User{})
err := cm.AutoCreateTable(db, &User{})
```

### PostgreSQL 示例
```go
// 创建 PostgreSQL 数据库连接
dataSource, _ := sql.Open("postgres", "user=user password=password dbname=dbname sslmode=disable")
db := db233.NewDbWithType(dataSource, 0, nil, db233.DatabaseTypePostgreSQL)

// 使用相同的实体定义
// 自动建表会生成 PostgreSQL 兼容的 SQL
err := cm.AutoCreateTable(db, &User{})
```

## 注意事项

1. **默认类型**：如果不指定数据库类型，默认使用 MySQL
2. **主键约束**：主键字段必须为 NOT NULL（所有数据库都要求）
3. **字段默认值**：默认允许字段为 NULL，除非明确标记 `not_null`
4. **复杂类型**：`map`、`slice`、`array` 类型会自动识别为 `TEXT` 类型（需要 JSON 序列化）

