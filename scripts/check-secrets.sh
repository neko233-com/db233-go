#!/usr/bin/env bash
# 提交/发版前检查：禁止将本地数据库凭据纳入 Git
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
FAIL=0

fail() { echo "FAIL: $1" >&2; FAIL=1; }
ok()   { echo "OK: $1"; }

FORBIDDEN=(config.local.json config.local.yaml config.local.yml)

for f in "${FORBIDDEN[@]}"; do
  if git ls-files --error-unmatch "$f" >/dev/null 2>&1; then
    fail "已纳入 Git 跟踪: $f — 请 git rm --cached $f"
  fi
done

while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  if [[ "$line" =~ \.local\.(json|ya?ml)$ ]] && [[ ! "$line" =~ \.example$ ]]; then
    fail "暂存区含本地配置: $line"
  fi
done < <(git diff --cached --name-only 2>/dev/null || true)

if git grep -I -n -E 'rm-[a-z0-9]+\.mysql\.rds\.aliyuncs\.com' -- ':!*.example' ':!scripts/check-secrets.*' >/dev/null 2>&1; then
  fail "仓库中发现疑似真实 RDS 主机名"
  git grep -I -n -E 'rm-[a-z0-9]+\.mysql\.rds\.aliyuncs\.com' -- ':!*.example' ':!scripts/check-secrets.*' >&2 || true
fi

if [[ $FAIL -ne 0 ]]; then
  echo ""
  echo "凭据只能放在 gitignore 的文件中: config.local.json / config.local.yaml / *.local.json"
  exit 1
fi

ok "未发现数据库凭据泄露风险"
