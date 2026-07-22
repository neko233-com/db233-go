# 统一 Schema 建表与迁移

`AutoMigrateSchema` 是普通实体表的统一生产入口。调用方只维护实体原型清单；建表、补列、索引、并发控制、数据库代次租约与最终验证由 db233-go 处理。

建议在服务启动、接收业务流量之前执行。迁移期间 API 会持有该 `Db` 的 generation 读租约，因此安全清库、切代和关闭会等待迁移结束。

## 最小接入

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

entities := []any{
    &PlayerEntity{},
    &InventoryEntity{},
}

report, err := database.AutoMigrateSchema(ctx, entities, nil)
if err != nil {
    // report 仍可能包含已经执行的动作和最终权威状态。
    return err
}
_ = report
```

默认权限仅允许：

- 创建缺失表；
- 添加缺失列；
- 创建缺失索引。

类型修改、列删除、索引替换与索引删除默认全部拒绝。迁移执行后仍不兼容时，返回可通过 `errors.Is(err, db233.ErrSchemaVerificationFailed)` 判断的错误，并同时保留报告。

## DryRun 与只读验证

```go
plan, err := database.AutoMigrateSchema(ctx, entities, &db233.SchemaMigrationOptions{
    DryRun: true,
})
// DryRun 不执行 DDL；预期漂移通过 plan.Before / plan.Tables 表达，不作为错误。

report, err := database.VerifySchema(ctx, entities, &db233.SchemaVerifyOptions{
    RequireExact: false, // 默认只要求兼容，允许业务未声明的安全扩展列/索引
})
if errors.Is(err, db233.ErrSchemaVerificationFailed) {
    // report 包含完整、稳定排序的问题清单。
}
```

`RequireExact: true` 还会把额外列和额外索引视为验证失败。验证 API 永不执行 DDL。

## 显式危险权限

```go
permissions := db233.DefaultSchemaMigrationPermissions()
permissions.UpdateColumn = true

report, err := database.AutoMigrateSchema(ctx, entities, &db233.SchemaMigrationOptions{
    MaxConcurrency: 4,
    Permissions:    &permissions,
})
```

危险权限应只在备份完成、变更已审阅且处于维护窗口时开启。MySQL DDL 会隐式提交，整个多表计划不是一个可回滚事务；失败报告会尽力重新读取数据库，准确展示已经生效的部分。

## 并发与幂等

- 默认并发度为 `DefaultSchemaConcurrency`，硬上限为 `MaxSchemaConcurrency`；同一表内动作始终串行。
- 同一 `Db` 的迁移/验证调用使用可取消编排锁。
- 不同进程同时启动时，DDL 冲突后会重新读取 metadata；只有实际结构与目标等价时才把该动作视为成功。
- 相同实体类型会去重；不同 Go 类型映射到同一表名会在访问数据库前失败。
- 表名、列名、索引名与显式 SQL 类型标签均先验证，拒绝多语句和注入字符。

## 实体约定

```go
type PlayerEntity struct {
    PlayerID string `db:"playerId,not_null" db_type:"varchar(64)" primary_key:"true"`
    Level    int    `db:"level,not_null"`
}

func (*PlayerEntity) TableName() string       { return "player" }
func (*PlayerEntity) SerializeBeforeSaveDb()  {}
func (*PlayerEntity) DeserializeAfterLoadDb() {}
```

索引继续通过 `GetTableMetaData() *db233.TableMetaData` 声明。实体的 `TableName`、metadata 和标签是唯一 schema 来源；调用方不再维护重复的 DDL、schema 快照或本地迁移缓存。

当前统一编排实现面向 MySQL。自定义建表策略必须实现 `IContextTableCreationStrategy`，否则严格拒绝，避免 context 取消后遗留不可控 metadata 查询。
