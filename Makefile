.PHONY: hooks test-hooks

# Install the git pre-commit hook for this clone.
hooks:
	git config core.hooksPath .githooks

# Table tests for the Claude Code secret guard and the git pre-commit hook.
test-hooks:
	bash .claude/hooks/guard-secrets_test.sh
	bash .githooks/pre-commit_test.sh
