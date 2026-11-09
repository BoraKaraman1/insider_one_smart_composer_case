# Smart Campaign Composer

A marketer writes a campaign brief. Claude proposes a target segment and SMS + email copy. A human reviews, edits and approves. Messages go through a Postgres-backed queue (River) to a mock provider, and a dashboard shows delivery status live. No real recipient is ever contacted.

Stack: Go backend, Postgres for data and queue, Vue 3 frontend, Claude API.

Read before changing anything in an area:
- `docs/ARCHITECTURE.md`: the design, and section 2 maps every rule below to its enforcement and its test.
- `DECISIONS.md`: why things are the way they are, plus the agent log. The user writes this file; agents read it and never edit it.
- `docs/segment-rules.md`: the segment rule language (the spec the compiler and validator implement).
- `docs/seed-data.md`: seed data shape and time anchoring.

## Non-negotiable rules

1. **The API key never enters a Claude session.**
   - Never read or print env files. Never expand `$ANTHROPIC_API_KEY`. Never run `docker compose config` or `docker inspect`, since both print env values.
   - Use `LLM_MODE=mock`. Tests never need the key.
   - If a task seems to need the key, stop and ask.
   - `.claude/settings.json` and `.claude/hooks/guard-secrets.sh` enforce this. Never weaken or work around them.
2. **LLM output is never run as SQL.** Segment rules are a JSON DSL compiled by `internal/segment`. Columns, operators and event types come only from Go allowlist maps, and every value is a bound parameter.
3. **Personalization variables come from an allowlist**: `first_name`, `last_name`, `city`, `campaign_url`. Nothing else renders.
4. **Prompt injection is handled by structure, not prompt wording.**
   - Briefs and model output are untrusted data.
   - The model has no tools and never sees recipient rows.
   - Consent is added by the compiler and is not expressible in the DSL.
   - No URL or email address is accepted from the model or from human edits; links come only through `{{campaign_url}}`.
5. **Every Claude output is validated** against `schemas/` plus semantic checks.
   - Failures are loud: logged, counted and shown.
   - Failures are retried with the errors sent back, at most 3 attempts.
   - Never truncate or default output to make it fit.
6. **Retries depend on the error class.**
   - Retry 408, 429 (honor `Retry-After`), 5xx, timeouts, refused and reset connections. Never retry any other 4xx.
   - Backoff is exponential with full jitter.
   - Stuck and failed messages are visible on the dashboard.
7. **Campaign approval is a state machine** with guarded transitions. Human edits go through the same validator as LLM output.
8. **Rate limit: 100 messages/s per channel.** Every provider call takes one token, retries included, with burst 1. It is tested with a fake clock (`testing/synctest`).
9. **No security-critical or contact data** in templates or the segment catalog: no password, token, email or phone.
10. **Delivery is at-least-once, with idempotency.** The idempotency key is the message id, and the provider must never send the same message twice.

## Commands

| Command | What it does |
|---|---|
| `make up` | `docker compose up` for the whole stack (migrate + seed run first) |
| `make test` | all tests against a throwaway Postgres; no API key, no network besides local Docker |
| `make test-unit` | tests that need no database |
| `make lint` | gofmt + go vet |
| `make hooks` | install the git pre-commit hook (`core.hooksPath .githooks`) |
| `make seed` / `make reseed` | load seed data / reload it anchored at now |

These targets are being added on day 1. If one is missing, it has not been built yet. Say so instead of inventing a replacement.

## Layout and module boundaries

```
cmd/app/            one binary, subcommands: api, worker, mockprovider, migrate, seed
internal/schema/    embedded JSON Schemas, full and API-derived, validator
internal/validation/ shared Issue{path, code, message} result type
internal/segment/   DSL types, catalog, validator, compiler, consent filter
internal/content/   template parser, renderer, SMS encoding and length
internal/campaign/  state machine, revisions
internal/delivery/  fan-out and send workers, retry classifier, backoff, rate limiter
internal/llm/       Claude client interface, real and fixture-replay implementations
internal/httpapi/   handlers, auth middleware, SSE
internal/store/     pgx queries, migrations runner
internal/seed/      deterministic seed generator and loader
internal/testdb/    per-package test database cloned from a template
internal/mockprovider/ fault-injecting provider with its own database
schemas/  migrations/  web/  docs/
```

- Packages talk through small interfaces and the shared contracts in `schemas/` and `docs/`, never through each other's internals. This keeps modules workable in parallel sessions.
- Each package owns its tests and its `testdata/`. Integration tests get their own database from `internal/testdb`.
- `internal/mockprovider` imports no app package and uses a separate database, so it cannot cheat.

## Conventions

- Go 1.25+, stdlib `net/http`, pgx v5, no ORM. SQL lives only in `internal/store` and the segment compiler.
- Errors returned to clients carry stable codes: `{path, code, message}`.
- Logging is `slog` JSON. Never log phone numbers, email addresses, message bodies or secrets.
- Code that depends on "now" takes it as a parameter (`asOf`). Tests use fixed times.
- Before adding a dependency, check it on pkg.go.dev (maintenance, license, exact version) and report what you found to the user in chat.
- Every change comes with tests. `make test` must pass with no API key.
- Prose in docs, comments and commit messages uses short sentences and no em dashes.

## Working with agents

- Agent output, including your own, is untrusted until a test or a check confirms it. When an agent gets something wrong, or a new decision comes up, report it to the user in chat: what was delegated, what was kept, what was wrong, and how it was caught. Do not edit `DECISIONS.md`; the user writes it.
- **The user commits.** Never run `git commit`, amend or push. Leave changes in the working tree and suggest a commit message.
- If a hook or permission rule blocks you, do not look for a way around it. Ask.

# Behavioral guidelines

Behavioral guidelines to reduce common LLM coding mistakes. Merge with project-specific instructions as needed.

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

## 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

## 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

## 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

## 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.

---

## 5. Parallel agentic development compatibility

**Every service and module is independent so could be worked on separate agentic sessions or by agent teams.**

---

**These guidelines are working if:** fewer unnecessary changes in diffs, fewer rewrites due to overcomplication, and clarifying questions come before implementation rather than after mistakes.
