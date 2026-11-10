#!/usr/bin/env bash
# Oturumun .env veya anahtarı okumasını keser. Çıkış 2 çağrıyı durdurur.
set -uo pipefail

block() {
  echo "guard-secrets: blocked ($1). Secrets stay out of Claude sessions; see .claude/settings.json and docs/ARCHITECTURE.md (Secrets)." >&2
  exit 2
}

command -v jq >/dev/null 2>&1 || block "jq is not installed, so the guard cannot inspect this call"

input=$(cat)
tool=$(jq -r '.tool_name // ""' <<<"$input" 2>/dev/null) || block "could not parse hook input"

# .env.example şablon olduğu için eşleşmeden önce çıkarılır.
env_ref='(^|[^A-Za-z0-9_])\.env([^A-Za-z0-9_]|$)'
key_like='sk-ant-[A-Za-z0-9_-]{8,}'

mentions_env_file() {
  local s=${1//.env.example/}
  grep -Eq -- "$env_ref" <<<"$s"
}

case "$tool" in
  Read|Edit|Write|MultiEdit|NotebookEdit)
    path=$(jq -r '.tool_input.file_path // .tool_input.notebook_path // ""' <<<"$input")
    if [[ -n "$path" ]] && mentions_env_file "$(basename -- "$path")"; then
      block "$tool on $(basename -- "$path")"
    fi
    if [[ "$tool" != "Read" ]]; then
      new_text=$(jq -r '.tool_input
        | [ .content, .new_string, .new_source, ((.edits // [])[] | .new_string) ]
        | map(select(type == "string")) | join("\n")' <<<"$input")
      if grep -Eq -- "$key_like" <<<"$new_text"; then
        block "new content looks like an Anthropic API key"
      fi
    fi
    ;;

  Grep)
    target=$(jq -r '[.tool_input.path // "", .tool_input.glob // ""] | join(" ")' <<<"$input")
    if mentions_env_file "$target"; then
      block "Grep targets an env file"
    fi
    ;;

  Bash)
    cmd=$(jq -r '.tool_input.command // ""' <<<"$input")

    mentions_env_file "$cmd" && block "command names an env file"
    grep -Eq -- "$key_like" <<<"$cmd" && block "command contains something that looks like an Anthropic API key"

    patterns=(
      '\$\{?(ANTHROPIC_API_KEY|ANTHROPIC_AUTH_TOKEN)'
      '(^|[^A-Za-z0-9_-])printenv([^A-Za-z0-9_-]|$)'
      '(^|[;&|(]|then|do)[[:space:]]*(env|export|set|declare|typeset)[[:space:]]*($|[;&|)>])'
      '(^|[^A-Za-z0-9_-])export[[:space:]]+-p'
      '(^|[^A-Za-z0-9_-])(declare|typeset)[[:space:]]+-[A-Za-z]*[xp]'
      '/proc/[^[:space:]]*/environ'
      '(^|[^A-Za-z0-9_-])ps[[:space:]]+(-[A-Za-z]*[eE]|[A-Za-z]*e)'
      'docker(-compose|[[:space:]]+compose)([[:space:]].*)?[[:space:]]config([[:space:]]|$)'
      'docker[[:space:]]+(container[[:space:]]+)?inspect([[:space:]]|$)'
      'docker.*[[:space:]]exec[[:space:]].*(^|[[:space:]])(env|printenv)([[:space:]]|$)'
    )
    for p in "${patterns[@]}"; do
      grep -Eq -- "$p" <<<"$cmd" && block "command could print the environment or the API key"
    done
    ;;
esac

exit 0
