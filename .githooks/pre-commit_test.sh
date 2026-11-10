#!/usr/bin/env bash
set -uo pipefail

HOOKS_DIR="${1:-$(cd "$(dirname "$0")" && pwd)}"
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null
# Parça parça ki depoda anahtar biçiminde düz metin durmasın.
fake_key="sk-""ant-""api03-abcdefghijklmnopqrstuv"

pass=0; fail=0
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

new_repo() {
  local repo
  repo=$(mktemp -d "$tmp/repo.XXXXXX")
  cd "$repo" || exit 1
  git init -q -b main
  git config user.name test
  git config user.email test@example.com
  git config core.hooksPath "$HOOKS_DIR"
  printf 'module example.com/t\n\ngo 1.25.0\n' > go.mod
  echo hi > README.md
  git add -A && git commit -q --no-verify -m init
}

expect() { # expect ok|blocked "label"
  local want="$1" label="$2" out got
  if out=$(git commit -q -m "$label" 2>&1); then got=ok; else got=blocked; fi
  if [[ "$got" == "$want" ]]; then
    pass=$((pass+1))
  else
    fail=$((fail+1)); echo "FAIL want=$want got=$got :: $label"; echo "$out" | sed 's/^/    /'
  fi
  if [[ "$out" == *"$fake_key"* ]]; then
    fail=$((fail+1)); echo "FAIL key value printed :: $label"
  fi
}

new_repo; echo x > a.txt; git add a.txt;                      expect ok      "clean text file"
new_repo; echo K=v > .env; git add .env;                      expect blocked "staged .env"
new_repo; echo K=v > .env.local; git add .env.local;          expect blocked "staged .env.local"
new_repo; mkdir config; echo K=v > config/.env.production; git add config; expect blocked "nested .env.production"
new_repo; echo K= > .env.example; git add .env.example;       expect ok      ".env.example is allowed"
new_repo; echo x > .envrc; echo x > env.md; git add -A;        expect ok      ".envrc and env.md are not env files"
new_repo; echo "k = $fake_key" > cfg.txt; git add cfg.txt;     expect blocked "key-shaped string added"
new_repo; printf 'a\nk = %s\n' "$fake_key" > cfg.txt; git add cfg.txt; git commit -q --no-verify -m leak
          echo a > cfg.txt; git add cfg.txt;                   expect ok      "removing a key-shaped line"
new_repo; printf 'package main\nfunc main(){}\n' > main.go; git add main.go; expect blocked "unformatted Go"
new_repo; printf 'package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Printf("%%d\\n", "x")\n}\n' > main.go
          git add main.go;                                     expect blocked "go vet failure"
new_repo; printf 'package main\n\nfunc main() {}\n' > main.go; git add main.go; expect ok "formatted valid Go"
new_repo; printf 'package main\nfunc main(){}\n' > main.go; git add main.go
          printf 'package main\n\nfunc main() {}\n' > main.go;  expect blocked "staged content checked, not the working tree"

echo "pass=$pass fail=$fail"
[[ $fail -eq 0 ]]
