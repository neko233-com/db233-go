# 数据库代次与安全清库

> 适用于 db233-go v1.1.0 及以上版本。

`DatabaseGeneration` 是逻辑数据库的持久化代次。生产游戏服必须把它保存于数据库内的独立元数据行，并在每次全量清库或重建时生成一个从未使用过的新值。它用于阻止旧 Session、WriteBuffer、WAL 和失败重试队列在清库后把历史玩家数据写回。

## 启动顺序

1. 建立连接并完成 schema 校验。
2. 从数据库读取或首次创建 generation；读取失败必须终止启动。
3. 把同一个非空值传给 `GameDbOptions.DatabaseGeneration`。
4. 调用 `InitGameDb`，之后才开放业务入口。

```go
snapshot, err := dataEpoch.EnsureInitialized(ctx, db.DataSource)
if err != nil {
    return fmt.Errorf("读取数据库代次: %w", err)
}

opts := db233.DefaultGameDbOptions()
opts.DatabaseGeneration = snapshot.EpochID
sessionRepo, err := db233.InitGameDb(db, dbConfig, opts)
```

首次从旧版本升级前应先优雅停服并排空旧 WAL/失败队列。没有 generation 元数据的遗留恢复文件无法证明属于当前数据库，会被隔离而不会自动回放。

## 运行中安全清库

清库前应先关闭登录入口并暂停业务写入，以缩短屏障等待时间。`BeginDatabaseGenerationTransition` 会原子地拒绝新的 db233 managed write，并严格排空已准入的 Session、WriteBuffer、WAL 与失败重试；调用方不需要在屏障外自行 Flush。

`Db.DataSource` 是兼容保留的原始 `database/sql` 入口，不受 db233 generation 租约控制。若业务存在 raw SQL、独立连接池或其他保存队列，必须先用业务侧 writer gate 暂停并排空，且该 gate 要一直持有到 `Commit`/`Abort`/`FailClosed` 返回。随后用 generation 屏障覆盖整个清库事务：

```go
newGeneration := generateUniqueEpoch()
transition, err := db.BeginDatabaseGenerationTransition(newGeneration)
if err != nil {
    return err
}

tx, err := db.DataSource.BeginTx(ctx, nil)
if err != nil {
    _ = transition.Abort()
    return err
}

if err := deletePlayerDataAndStoreEpoch(ctx, tx, newGeneration); err != nil {
    if rollbackErr := tx.Rollback(); rollbackErr != nil {
        return transition.FailClosed(errors.Join(err, rollbackErr))
    }
    _ = transition.Abort()
    return err
}
if err := tx.Commit(); err != nil {
    // Commit 结果可能未知，禁止猜测旧代或新代。
    return transition.FailClosed(err)
}
if err := transition.Commit(); err != nil {
    // 数据库已提交但本地隔离失败；Db 会保持 fail-closed，服务必须退出。
    return err
}
```

严格要求：

- 玩家表删除和数据库 generation 更新必须在同一个事务内完成。
- `BeginDatabaseGenerationTransition` 必须早于 `DELETE`；提交后再调用 `RotateDatabaseGeneration` 不安全。
- 屏障只自动覆盖 db233 managed write；直接使用 `Db.DataSource` 的路径必须由调用方 writer gate 覆盖。
- 旧 Session 在切代后失效；业务恢复入口前必须按新代重新打开 Session。
- 数据库事务未提交且成功回滚时才允许 `Abort`。
- `tx.Commit()` 返回错误属于 commit-unknown，必须 `FailClosed` 并受控退出。
- `transition.Commit()` 失败后禁止重新开放登录；重启时以数据库内 generation 为准。
- 数据库身份/租约/代次元数据表不能加入玩家清档表集合。

## 外部重建

外部脚本必须先优雅停止旧进程，并确认唯一写者租约已释放，再 DROP/重建整个逻辑数据库。若只删除玩家表，则必须在同一事务内轮换数据库 generation。保留原 generation 的局部清表无法与旧恢复数据区分，生产环境禁止这样做。
