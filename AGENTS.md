
# 编写规范

## 编码

**必须是 utf-8**

## 数据库凭据（安全）

- **禁止提交**真实连接信息；仅允许占位符的 `config.local.json.example` / `config.local.yaml.example`
- 本地真实配置文件名：`config.local.json` 或 `config.local.yaml`（已在 `.gitignore`）
- 发版/推送前运行：`./scripts/check-secrets.ps1` 或 `./scripts/check-secrets.sh`