# Agent instructions

Read `CLAUDE.md` before doing anything. Every rule there applies to every agent working in this repo (Claude, Codex, Cursor and others), not only Claude Code.

One rule needs repeating here, because its enforcement only exists for Claude Code (`.claude/settings.json` and `.claude/hooks/guard-secrets.sh`):

- **Never read or print env files or the Anthropic API key.** Do not open `.env`. Do not expand `$ANTHROPIC_API_KEY`. Do not run `printenv`, `env`, `docker compose config` or `docker inspect`. Use `LLM_MODE=mock`; tests never need the key. If a task seems to need the key, stop and ask.

Also: never run `git commit`, amend or push. The user commits.
