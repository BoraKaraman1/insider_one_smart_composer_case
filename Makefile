.PHONY: up seed reseed test test-unit test-hooks lint hooks web-test e2e eval

TEST_PG := postgres://postgres:test@127.0.0.1:55432
TEMPLATE_URL := $(TEST_PG)/app_template?sslmode=disable

TAGS ?=

up:
	docker compose up --build

seed:
	docker compose run --rm seed
reseed:
	docker compose run --rm seed seed -reset

test:
	docker compose --profile test up -d --wait postgres-test
	@trap 'docker compose --profile test rm -sf postgres-test >/dev/null 2>&1' EXIT; set -e; \
	docker compose exec -T postgres-test psql -q -U postgres -c 'DROP DATABASE IF EXISTS app_template' -c 'CREATE DATABASE app_template'; \
	DATABASE_URL='$(TEMPLATE_URL)' go run ./cmd/app migrate; \
	DATABASE_URL='$(TEMPLATE_URL)' go run ./cmd/app seed -as-of 2026-01-01T00:00:00Z -seed 42 -users 5000; \
	env -u ANTHROPIC_API_KEY TEST_DATABASE_URL='$(TEST_PG)/postgres?sslmode=disable' REQUIRE_DB=1 go test -count=1 -tags '$(TAGS)' ./...; \
	$(MAKE) -s test-hooks

eval:
	docker compose --profile test up -d --wait postgres-test
	@trap 'docker compose --profile test rm -sf postgres-test >/dev/null 2>&1' EXIT; set -e; \
	docker compose exec -T postgres-test psql -q -U postgres -c 'DROP DATABASE IF EXISTS app_template' -c 'CREATE DATABASE app_template'; \
	DATABASE_URL='$(TEMPLATE_URL)' go run ./cmd/app migrate; \
	DATABASE_URL='$(TEMPLATE_URL)' go run ./cmd/app seed -as-of 2026-01-01T00:00:00Z -seed 42 -users 5000; \
	LLM_MODE="$${LLM_MODE:-mock}" EVAL_ADMIN_URL='$(TEST_PG)/postgres?sslmode=disable' go run ./cmd/app eval

web-test:
	npm --prefix web ci
	npm --prefix web run typecheck
	npm --prefix web test

e2e:
	npm --prefix web ci
	npm --prefix web run e2e

test-unit:
	env -u ANTHROPIC_API_KEY go test ./...

test-hooks:
	bash .claude/hooks/guard-secrets_test.sh
	bash .githooks/pre-commit_test.sh

lint:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...

hooks:
	git config core.hooksPath .githooks
