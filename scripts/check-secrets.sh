#!/usr/bin/env bash
# 提交/发版前检查：扫描已跟踪与未跟踪（但未忽略）的文件。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
FAIL=0

fail() { printf 'FAIL: %s\n' "$1" >&2; FAIL=1; }
ok()   { printf 'OK: %s\n' "$1"; }

mapfile -d '' FILES < <(git ls-files -z --cached --others --exclude-standard)

matching_files() {
  local pattern="$1"
  local output
  local code
  if output="$(git grep --untracked --exclude-standard -I -l -E -e "$pattern" -- . ':!scripts/check-secrets.*' 2>/dev/null)"; then
    printf '%s' "$output"
    return 0
  else
    code=$?
    [[ $code -eq 1 ]] && return 0
    return "$code"
  fi
}

scan_files() {
  local name="$1"
  local pattern="$2"
  local matches
  local path
  matches="$(matching_files "$pattern")"
  while IFS= read -r path; do
    [[ -n "$path" ]] || continue
    # 禁止输出匹配内容，避免门禁日志二次泄密。
    fail "疑似${name}: ${path}"
  done <<< "$matches"
}

for path in "${FILES[@]}"; do
  if [[ "$path" =~ (^|/)(config\.local\.(json|ya?ml)|[^/]+\.local\.(json|ya?ml))$ ]] &&
     [[ ! "$path" =~ \.example$ ]]; then
    fail "发现未忽略的本地凭据文件: $path"
  fi
done

# 仅匹配高置信度格式。模式定义文件自身已从内容扫描排除。
scan_files "私钥" '-----BEGIN (RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----'
scan_files "AWS access key" '(AKIA|ASIA)[0-9A-Z]{16}'
scan_files "GitHub token" '(gh[pousr]_[A-Za-z0-9_]{30,}|github_pat_[A-Za-z0-9_]{20,})'
scan_files "GitLab token" 'glpat-[A-Za-z0-9_-]{20,}'
scan_files "OpenAI key" 'sk-(live|proj|svcacct|admin)-[A-Za-z0-9_-]{16,}'
scan_files "Slack token" 'xox[baprs]-[A-Za-z0-9-]{20,}'
scan_files "Google API key" 'AIza[0-9A-Za-z_-]{35}'
scan_files "带凭据 URL" 'https?://[^/@[:space:]]+:[^/@[:space:]]+@'
scan_files "真实 RDS 主机名" 'rm-[a-z0-9]+\.mysql\.rds\.aliyuncs\.com'
BUSINESS_REPO_PATTERN='server-project-''sf-go'
scan_files "业务仓库名称" "$BUSINESS_REPO_PATTERN"

ip_pattern='([0-9]{1,3}\.){3}[0-9]{1,3}'
ip_files="$(matching_files "$ip_pattern")"
while IFS= read -r path; do
  [[ -n "$path" ]] || continue
  [[ -f "$path" ]] || continue
  while IFS= read -r line || [[ -n "$line" ]]; do
    remainder="$line"
    while [[ "$remainder" =~ $ip_pattern ]]; do
      ip="${BASH_REMATCH[0]}"
      [[ "$ip" == "127.0.0.1" ]] || fail "非 loopback IPv4 字面量: ${path}"
      remainder="${remainder#*"$ip"}"
    done
  done < "$path"
done <<< "$ip_files"

dsn_pattern='([A-Za-z0-9_.%+-]+):([^@[:space:]]+)@tcp\(([^):]+)(:[0-9]+)?\)'
dsn_files="$(matching_files "$dsn_pattern")"
while IFS= read -r path; do
  [[ -n "$path" ]] || continue
  [[ -f "$path" ]] || continue
  while IFS= read -r line || [[ -n "$line" ]]; do
    remainder="$line"
    while [[ "$remainder" =~ $dsn_pattern ]]; do
      match="${BASH_REMATCH[0]}"
      host="${BASH_REMATCH[3]}"
      if [[ "$host" != "127.0.0.1" && "${host,,}" != "localhost" ]]; then
        fail "带凭据的非本机数据库 DSN: ${path}"
      fi
      remainder="${remainder#*"$match"}"
    done
  done < "$path"
done <<< "$dsn_files"

if [[ $FAIL -ne 0 ]]; then
  echo "" >&2
  echo "凭据只能放在被 .gitignore 排除的本地配置或秘密管理系统中。" >&2
  exit 1
fi

ok "未发现本地配置、私钥、高置信度令牌、远端凭据 URL/DSN 或非本机 IP"
