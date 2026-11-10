.PHONY: up seed reseed test test-unit test-hooks lint hooks web-test e2e eval

# Throwaway test server from docker-compose.yml (profile "test", in memory).
TEST_PG := postgres://postgres:test@127.0.0.1:55432
TEMPLATE_URL := $(TEST_PG)/app_template?sslmode=disable

# Build tags for go test. TAGS=spec adds the third plan's test-first specs
# (files with //go:build spec), which stay red until their stream lands.
TAGS ?=

# The whole stack: migrate, seed (anchored at now), api.
up:
	docker compose up --build

# Load seed data into the running stack; reseed re-anchors it at now and
# wipes campaigns (docs/seed-data.md).
seed:
	docker compose run --rm seed
reseed:
	docker compose run --rm seed seed -reset

# Every test: unit, integration (against a throwaway Postgres) and hooks.
# Builds app_template once (migrated, seeded at a fixed anchor); each
# integration package clones it. No API key, no network besides local Docker.
test:
	docker compose --profile test up -d --wait postgres-test
	@trap 'docker compose --profile test rm -sf postgres-test >/dev/null 2>&1' EXIT; set -e; \
	docker compose exec -T postgres-test psql -q -U postgres -c 'DROP DATABASE IF EXISTS app_template' -c 'CREATE DATABASE app_template'; \
	DATABASE_URL='$(TEMPLATE_URL)' go run ./cmd/app migrate; \
	DATABASE_URL='$(TEMPLATE_URL)' go run ./cmd/app seed -as-of 2026-01-01T00:00:00Z -seed 42 -users 5000; \
	env -u ANTHROPIC_API_KEY TEST_DATABASE_URL='$(TEST_PG)/postgres?sslmode=disable' REQUIRE_DB=1 go test -count=1 -tags '$(TAGS)' ./...; \
	$(MAKE) -s test-hooks

# Golden briefs. Mock unless LLM_MODE is already set. LLM_MODE=anthropic is
# for a human terminal only; this recipe never prints a key. The report is
# evals/report-<mode>.md. Same seeded template as make test.
eval:
	docker compose --profile test up -d --wait postgres-test
	@trap 'docker compose --profile test rm -sf postgres-test >/dev/null 2>&1' EXIT; set -e; \
	docker compose exec -T postgres-test psql -q -U postgres -c 'DROP DATABASE IF EXISTS app_template' -c 'CREATE DATABASE app_template'; \
	DATABASE_URL='$(TEMPLATE_URL)' go run ./cmd/app migrate; \
	DATABASE_URL='$(TEMPLATE_URL)' go run ./cmd/app seed -as-of 2026-01-01T00:00:00Z -seed 42 -users 5000; \
	LLM_MODE="$${LLM_MODE:-mock}" EVAL_ADMIN_URL='$(TEST_PG)/postgres?sslmode=disable' go run ./cmd/app eval

# Frontend typecheck and unit tests. Needs Node 22.
web-test:
	npm --prefix web ci
	npm --prefix web run typecheck
	npm --prefix web test

# Nginx scenarios against the compose stack on http://127.0.0.1:8081.
# Start the stack first (make up). Mock mode, no API key.
e2e:
	npm --prefix web ci
	npm --prefix web run e2e

# Tests that need no database (integration tests skip).
test-unit:
	env -u ANTHROPIC_API_KEY go test ./...

# Table tests for the Claude Code secret guard and the git pre-commit hook.
test-hooks:
	bash .claude/hooks/guard-secrets_test.sh
	bash .githooks/pre-commit_test.sh

lint:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...

# Install the git pre-commit hook for this clone.
hooks:
	git config core.hooksPath .githooks
