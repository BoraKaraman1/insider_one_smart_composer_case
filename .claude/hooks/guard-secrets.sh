#!/usr/bin/env bash
# PreToolUse guard: keeps secrets (.env files and the Anthropic API key) out of
# Claude Code sessions. This is the second layer behind permissions.deny in
# .claude/settings.json.
#
# Text matching is not a security boundary: a script that opens .env by itself
# is not caught. It does catch the usual ways a session reads or prints the key.
# The real guarantee is that nothing in this repo needs the key at dev or test
# time (tests use the mock LLM), so a session never has a reason to touch it.
#
# Exit 2 blocks the call and shows stderr to Claude. Exit 0 means no decision.
set -uo pipefail

block() {
  echo "guard-secrets: blocked ($1). Secrets stay out of Claude sessions; see .claude/settings.json and docs/ARCHITECTURE.md (Secrets)." >&2
  exit 2
}

# Fail closed: if we cannot inspect the call, we do not allow it.
command -v jq >/dev/null 2>&1 || block "jq is not installed, so the guard cannot inspect this call"

input=$(cat)
tool=$(jq -r '.tool_name // ""' <<<"$input" 2>/dev/null) || block "could not parse hook input"

# Matches .env, .env.local, .env.production, ... anywhere in a string.
# .env.example is removed before matching because it is the committed template.
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
      # Expanding the key or token into a command.
      '\$\{?(ANTHROPIC_API_KEY|ANTHROPIC_AUTH_TOKEN)'
      # Dumping the environment.
      '(^|[^A-Za-z0-9_-])printenv([^A-Za-z0-9_-]|$)'
      '(^|[;&|(]|then|do)[[:space:]]*(env|export|set|declare|typeset)[[:space:]]*($|[;&|)>])'
      '(^|[^A-Za-z0-9_-])export[[:space:]]+-p'
      '(^|[^A-Za-z0-9_-])(declare|typeset)[[:space:]]+-[A-Za-z]*[xp]'
      '/proc/[^[:space:]]*/environ'
      # macOS ps: -e / e prints each process environment.
      '(^|[^A-Za-z0-9_-])ps[[:space:]]+(-[A-Za-z]*[eE]|[A-Za-z]*e)'
      # docker compose config renders .env values into the output.
      'docker(-compose|[[:space:]]+compose)([[:space:]].*)?[[:space:]]config([[:space:]]|$)'
      # docker inspect shows container env vars.
      'docker[[:space:]]+(container[[:space:]]+)?inspect([[:space:]]|$)'
      # Printing env inside a container.
      'docker.*[[:space:]]exec[[:space:]].*(^|[[:space:]])(env|printenv)([[:space:]]|$)'
    )
    for p in "${patterns[@]}"; do
      grep -Eq -- "$p" <<<"$cmd" && block "command could print the environment or the API key"
    done
    ;;
esac

exit 0
