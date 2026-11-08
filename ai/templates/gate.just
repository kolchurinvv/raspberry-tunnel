# Gate recipes — the contract the rest of the kit assumes.
# Copy into your justfile (or translate to Makefile / npm scripts). The names
# matter: AGENTS.md, the pre-commit hook, and the CI-watch discipline all refer
# to `check`, `precommit`, `hook-install`, and `ci-watch`.

# Fast aggregate — run before every commit. No heavyweight E2E.
# Compose from whatever the project actually has; every entry is a hard gate.
check: typecheck lint format-check test
    @echo "→ check: all fast gates green"

# Strict aggregate — run before pushing user-facing changes. check + slow suite.
# This is what the pre-commit hook delegates to.
precommit: check
    @echo "→ precommit: running slow suite (E2E, snapshot diff)…"
    # just e2e
    # just screenshot-diff

# Wire the delegating pre-commit hook into git. One-time per clone.
hook-install:
    git config core.hooksPath .githooks
    @echo "→ pre-commit hook installed. Bypass one commit with --no-verify."

hook-uninstall:
    git config --unset core.hooksPath

# Watch a CI run to a terminal conclusion. THIS recipe is the poll loop —
# callers must not wrap it in their own while/sleep loop (ai/02-ci-discipline.md).
ci-watch *ARGS:
    ./scripts/ci-watch.sh {{ ARGS }}
