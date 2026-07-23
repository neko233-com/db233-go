# Entity 版本化数据迁移

`AutoMigrateEntityLifecycle` 将 Entity 自动建表和业务数据升级组成一个可审计的生产流程：

1. **PreSchema**：创建表、增加列/索引、执行明确允许的安全改列。
2. **DataMigration**：按全局 `Order` 执行版本化 Go 回调。
3. **Verify**：在同一事务内验证迁移结果。
4. **FinalizeSchema**：仅在业务显式授权时删除旧列/索引。
5. **FinalSchema**：重新验证最终结构。

默认不执行第 4 步。跨版本滚动发布应先让新旧程序都能运行，再在后续版本显式收缩。类型不兼容时不要直接改原列：新增影子列、迁移并验证数据、切换 Entity 映射，最后删除旧列。

## Go 迁移声明

```go
migrations := []db233.EntityDataMigration{{
    Scope:       "PlayerEntity",
    Version:     2,
    Order:       2026072301,
    Name:        "copy_old_score_to_score_v2",
    Fingerprint: "update-score-v2-and-verify-zero-mismatch",
    Up: func(ctx context.Context, tx *db233.EntityDataMigrationTx) error {
        _, err := tx.ExecContext(ctx, `
            UPDATE PlayerEntity
            SET scoreV2 = oldScore
            WHERE scoreV2 IS NULL`)
        return err
    },
    Verify: func(ctx context.Context, tx *db233.EntityDataMigrationTx) error {
        var count int
        if err := tx.QueryRowContext(ctx, `
            SELECT COUNT(*) FROM PlayerEntity
            WHERE scoreV2 IS NULL`).Scan(&count); err != nil {
            return err
        }
        if count != 0 {
            return fmt.Errorf("仍有 %d 行未迁移", count)
        }
        return nil
    },
}}
```

`Scope + Version` 和 `Order` 必须唯一。已上线迁移不可修改；需要修正时新增版本。`Fingerprint` 应描述迁移算法和校验规则，db233 会将声明计算为 SHA-256 并与数据库记录核对，防止旧迁移被静默改写。

## 启动编排

```go
permissions := db233.SchemaMigrationPermissions{
    CreateTable: true, CreateColumn: true, CreateIndex: true,
    UpdateColumn: true, ReplaceIndex: true,
}

report, err := database.AutoMigrateEntityLifecycle(ctx, entities,
    &db233.EntitySchemaLifecycleOptions{
        Namespace:            "game",
        PreSchemaPermissions: &permissions,
        DataMigrations:       migrations,
    })
```

生产不应默认设置 `FinalizePermissions.DeleteColumn/DeleteIndex`。删除动作应作为明确的收缩版本发布，并在旧程序不再运行、迁移校验已通过后启用。

改列、删列、替换索引和删索引必须同时开启分类权限并精确点名目标：

```go
finalize := db233.DefaultSchemaMigrationPermissions()
finalize.DeleteColumn = true
finalize.AllowedDeleteColumns = []db233.SchemaMigrationTarget{{
    Table: "PlayerEntity", Object: "legacyScore",
}}
```

只设置 `DeleteColumn=true` 不会删除任何列。新增表、列、索引仍使用安全默认自动完成。

## 原子性、并发与失败

- MySQL advisory lock 保证同一数据库、同一 Namespace 只有一个实例迁移。
- 每个 `Up + Verify + 审计记录` 在同一事务提交；任一步失败会全部回滚并阻止启动。
- 回调只开放受控事务，`ExecContext` 拒绝 DDL 和事务控制。
- 数据库存在当前程序未声明的已应用版本时默认失败，防止错误回滚旧二进制。
- 生命周期编排持有一个锁连接，同时 schema 阶段使用连接池，因此连接池至少需要 2 个连接。

## 数据库版本

db233 将已应用记录写入 `db233_entity_migrations`。业务可读取全局版本和每个 Entity 的版本：

```go
state, err := database.GetEntityMigrationState(ctx, "game")
// state.CurrentOrder
// state.ScopeVersions["PlayerEntity"]
// state.AppliedCount
// state.LastAppliedAt
```

`EntitySchemaLifecycleReport.Version` 也会返回迁移完成后的同一份版本快照。尚无迁移记录时返回零版本。

业务工程应在自己的 `db_version_migration/` 中维护版本实体和 V1、V2、V3... 声明。db233 只提供通用引擎与权威审计表，避免业务每新增一个版本都要重新发布 ORM。
