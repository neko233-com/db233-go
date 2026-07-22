# db233-go 生产发布流程

发布必须遵循 **PR → 审核 → CI 全绿 → 合并 main → 不可变标签 → GitHub Release**。禁止从功能分支直推、跳过测试、强推 main、覆盖或删除已发布标签。

## 1. 在 PR 中准备版本

1. 按 SemVer 更新 `version.txt`，格式必须为 `vX.Y.Z`。
2. 更新 `CHANGELOG.md` 和受影响文档。
3. 运行本地门禁：

```powershell
./scripts/check-secrets.ps1
gofmt -w .
git diff --check
go mod verify
go build ./...
go vet ./...
go run github.com/kisielk/errcheck@v1.20.0 -ignoretests ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 '-checks=SA*,S*,-ST*' ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
go test ./... -shuffle=on -count=3 -timeout=10m
```

Linux/macOS 的凭据门禁使用：

```bash
bash ./scripts/check-secrets.sh
```

4. 创建 PR，经 `gh pr diff` 审核，并等待 GitHub Actions 的 Linux 集成、race detector、benchmark gate 和 Windows 门禁全部成功。
5. 合并 PR。版本号和发布说明不得在合并后临时改写。

## 2. 从已同步的 main 发布

```powershell
git switch main
git pull --ff-only origin main
./publish.ps1 -DryRun
./publish.ps1
```

`publish.ps1` 会重新执行 secrets、格式、依赖、build、vet、errcheck、staticcheck、govulncheck、重复测试、benchmark 和 GoReleaser 配置检查，并验证当前 HEAD 与 `origin/main` 完全一致、对应 GitHub CI 已成功。随后只推送 `version.txt` 指定的精确标签，并通过 `gh` 创建 Release；脚本不会修改 remote、提交代码、推送分支或批量推送其他标签。

若标签已正确推送但 Release 创建因网络中断失败，人工确认标签确实指向当前 main 后执行：

```powershell
./publish.ps1 -Resume
```

## 3. 发布后验证

```powershell
$version = (Get-Content version.txt -Raw).Trim()
gh release view $version --repo neko233-com/db233-go
go list -m -versions github.com/neko233-com/db233-go
```

确认 Release 可见、标签指向合并提交，并验证下游模块能解析新版本。已公开版本视为不可变；如有问题，修复后发布更高版本，不覆盖旧标签。
