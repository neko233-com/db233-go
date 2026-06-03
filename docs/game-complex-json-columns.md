# 游戏项目复杂数据列接入规范

本文给接入游戏项目的 agent 使用，目标是让实体中的背包、英雄、任务进度等复杂数据安全落到数据库的一列中。

db233-go 支持把 `map` / `slice` / `array` / 普通 `struct` 字段自动序列化为 JSON 字符串保存到 `MEDIUMTEXT` / `JSON` / `LONGTEXT` 等字符串列中；查询实体时会把数据库中的 JSON 字符串自动反序列化回对应 Go 字段，然后再调用实体的 `DeserializeAfterLoadDb()`。

注意：`db_type` 是显式建表类型，db233-go 不会替你把 `TEXT` 改成 `MEDIUMTEXT`。MySQL `TEXT` 只有 64KB 级别容量，英雄、背包、任务等 JSON 可能达到几 MB 时，应显式使用 `db_type:"MEDIUMTEXT"`；如果单列可能超过 16MB，再使用 `db_type:"LONGTEXT"`。

## 推荐写法：复杂字段直接映射到一列

适用于新接入项目，或可以调整实体字段类型的项目。

```go
type HeroBo struct {
	HeroID int64  `json:"heroId"`
	Level  int    `json:"level"`
	Star   int    `json:"star"`
	Name   string `json:"name"`
}

type PlayerHeroEntity struct {
	PlayerID string             `db:"player_id" primary_key:"true"`
	HeroMap  map[string]*HeroBo `db:"hero_map" db_type:"MEDIUMTEXT"`
	HeroList []*HeroBo          `db:"hero_list" db_type:"MEDIUMTEXT"`
}

func (e *PlayerHeroEntity) TableName() string {
	return "player_hero"
}

func (e *PlayerHeroEntity) SerializeBeforeSaveDb() {
	if e.HeroMap == nil {
		e.HeroMap = map[string]*HeroBo{}
	}
	if e.HeroList == nil {
		e.HeroList = []*HeroBo{}
	}
}

func (e *PlayerHeroEntity) DeserializeAfterLoadDb() {
	if e.HeroMap == nil {
		e.HeroMap = map[string]*HeroBo{}
	}
	if e.HeroList == nil {
		e.HeroList = []*HeroBo{}
	}
}
```

对应表结构：

```sql
CREATE TABLE player_hero (
	player_id VARCHAR(64) NOT NULL,
	hero_map MEDIUMTEXT,
	hero_list MEDIUMTEXT,
	PRIMARY KEY (player_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

保存路径：

- `Save`
- `SaveBatch`
- `SaveBatchUpsert`
- `UpdateBatchUpsert`
- `SaveBuffered` / `FlushWriteBuffer`

这些路径会在真正生成字段值前调用 `SerializeBeforeSaveDb()`，随后 db233-go 自动把复杂字段 JSON 序列化成字符串写入数据库。

查询路径：

- `FindById`
- `FindByIds`
- `FindAll`
- `FindByCondition`
- `FindByIdConcurrent`（内部走 `FindById`）
- `PlayerSession.GetOrLoad`（内部走 repository 查询）

这些路径会先把数据库 JSON 字符串还原为实体字段，再调用 `DeserializeAfterLoadDb()`。如果数据库列是空字符串，`map` 会初始化为空 map，`slice` 会初始化为空 slice，避免首次创建玩家时业务字段为 nil。

## 兼容写法：数据库字段是 string，业务字段不入库

适用于已有项目已经使用 `HeroMapJson string` 存库，业务层仍希望操作 `map[string]*HeroBo`。

```go
type PlayerHeroEntity struct {
	PlayerID    string             `db:"player_id" primary_key:"true"`
	HeroMapJson string             `db:"hero_map" db_type:"MEDIUMTEXT"`
	HeroMap     map[string]*HeroBo `db:"-"`
}

func (e *PlayerHeroEntity) TableName() string {
	return "player_hero"
}

func (e *PlayerHeroEntity) SerializeBeforeSaveDb() {
	e.HeroMapJson = db233.ToJSONStringOrDefault(e.HeroMap, "{}")
}

func (e *PlayerHeroEntity) DeserializeAfterLoadDb() {
	e.HeroMap = db233.GetOrCreateDefault(e.HeroMapJson, map[string]*HeroBo{})
}
```

使用兼容写法时，业务文件需要引入 db233：

```go
import "github.com/neko233-com/db233-go/pkg/db233"
```

业务字段必须标记 `db:"-"`，否则会多映射一列。

## 辅助方法

`GetOrCreateDefault` 用于已有 string JSON 列，避免空字符串、`null`、坏 JSON 让业务 map/slice 变成 nil：

```go
heroMap := db233.GetOrCreateDefault(heroMapJson, map[string]*HeroBo{})
heroList := db233.GetOrCreateDefault(heroListJson, []*HeroBo{})
```

`ToJSONStringOrDefault` 用于保存前把业务字段写回 string 列：

```go
heroMapJson := db233.ToJSONStringOrDefault(heroMap, "{}")
heroListJson := db233.ToJSONStringOrDefault(heroList, "[]")
```

## 接入约束

- 需要入库的字段必须有 `db:"column_name"` 标签；没有 `db` 标签不会保存。
- 主键字段使用 `primary_key:"true"`，自增主键再加 `auto_increment:"true"`。
- 未写 `db_type` 的复杂字段默认建表使用 `MEDIUMTEXT`，可存 2MB+ JSON。
- 显式 `db_type` 会原样使用；不要给英雄/背包大 JSON 写 `db_type:"TEXT"`，`TEXT` 容量太小。
- 如果单列可能超过 16MB，显式标记 `db_type:"LONGTEXT"`。
- `map` 的 key 必须是 JSON 支持的 key 类型，游戏业务推荐 `string`；`map[int]T` 也可读写，但 JSON 中会以字符串形式存储 key。
- 复杂对象需要导出字段，并加 `json` 标签，避免字段名变更造成线上数据不兼容。
- 钩子里不要访问数据库；只做内存字段整理、默认值补齐、兼容旧 JSON 等轻量逻辑。

## 接入验收

新增实体后至少补一个 round-trip 测试：

1. 创建测试表，复杂列使用 `MEDIUMTEXT`。
2. 构造含 `map[string]*HeroBo` / `[]*HeroBo` 的实体。
3. 调用 `Save` 或 `UpdateBatchUpsert`。
4. 调用 `FindById` 读回。
5. 校验 map 中的 hero id、等级、名称等业务字段完整一致。

db233-go 自身的回归测试可参考 `TestHeroCollectionJSONRoundTrip`。
