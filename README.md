# db233-go

db233-go = ORM + Sharding + Flyway + Db Metrics

## 安装

```bash
go get github.com/SolarisNeko/db233-go
```

## 使用

```go
package main

import (
    "github.com/SolarisNeko/db233-go/pkg/db233"
)

func main() {
    manager := db233.GetInstance()
    // 初始化你的数据库组
}
```

## 发布

运行 `publish.cmd` (Windows) 或 `publish.ps1` (PowerShell) 来一键发布新版本。