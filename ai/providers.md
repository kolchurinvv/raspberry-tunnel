# Provider adapters

The kit is provider-agnostic: the *content* lives in `AGENTS.md` + `ai/*.md`,
and each AI tool reads it through whatever entrypoint that tool expects. **One
source of truth, many thin entrypoints.** Never fork the content per provider —
point each provider at it.

## Entrypoint filenames by provider

| Provider / tool | Entrypoint it auto-reads | Sub-agent dir | Settings/permissions |
|---|---|---|---|
| **Claude Code** | `CLAUDE.md`; `AGENTS.md` only when no `CLAUDE.md` exists (v2.1.277+, not every backend) | `.claude/agents/*.md` | `.claude/settings.json` (shared, tracked), `.claude/settings.local.json` (local; track it if you want grants shared) |
| **Cursor** | `AGENTS.md` (also `.cursor/rules/*.mdc`) | — | `.cursor/rules/` |
| **GitHub Copilot** | coding agent: `AGENTS.md`; IDE chat: `.github/copilot-instructions.md` | — | — |
| **Gemini CLI** | `GEMINI.md` (other names via its `context.fileName` setting) | — | — |
| **OpenAI Codex / Aider / Zed / others** | `AGENTS.md` | varies | varies |

## Wire the entrypoints

Copying the kit and writing the Bindings is `ai/README.md` § Adopt. What's left
per tool is making its entrypoint load `AGENTS.md` instead of duplicating it:

- **Claude Code — import, `@AGENTS.md`.** A `CLAUDE.md` whose first line is
  `@AGENTS.md` inlines it at session start on every Claude Code version and
  backend. Without one, only recent versions read `AGENTS.md`, and only while no
  `CLAUDE.md` exists anywhere from the working directory up, so the first
  `CLAUDE.md` anyone adds silently switches `AGENTS.md` off. Not a symlink:
  Claude's own edit tools refuse to write through it, and Windows clones break it.
  Not a stub that says "See AGENTS.md": Claude sees the content only if it
  decides to open the file.
- **Copilot IDE chat:** `mkdir -p .github && ln -s ../AGENTS.md
  .github/copilot-instructions.md` (it has no import syntax). Its coding agent
  reads `AGENTS.md` itself.
- **Gemini CLI:** point its `context.fileName` setting at `AGENTS.md`, or
  `ln -s AGENTS.md GEMINI.md`.
- **Cursor, Codex, Zed, and most others** read `AGENTS.md` natively.

Add an entrypoint only for a tool somebody actually uses on the repository.
Every extra entrypoint is a file somebody has to recognise as generated.

## Per-provider specifics

### Claude Code
- `CLAUDE.md` opens with `@AGENTS.md`, then adds only Claude-Code-specific
  deltas: scratchpad goes in repo-local `.tmp/` not `/tmp`; CI watching runs
  under the Monitor tool; skills/plan-mode/task conventions. A repo-local skill
  that implements a module (e.g. an adversarial-review skill for `ai/08`) is
  named in Bindings, so agents in other tools know it exists. It points at the
  module for the rules instead of restating them, so an update reaches it.
- **Permissions** are the autonomy lever. `ai/templates/claude.settings.json`
  shows a yolo-but-guarded profile: broad `allow`, with a `deny` list that
  protects baselines and forbids writes to sibling repos. Autonomy is granted
  *here*, conditional on the execution policy (`ai/00`).
- **Sub-agents** go in `.claude/agents/*.md` with frontmatter `name`,
  `description`, optional `model`, `color`, `memory` (see § Loading the kit's
  agents). Track them.
- CI-watch and other recurring read-only commands belong in the `allow` list so
  they don't burn an approval round-trip each run.

### Cursor
- Reads `AGENTS.md` directly. For rules that should always-apply or glob-scope,
  mirror them into `.cursor/rules/*.mdc` with `alwaysApply: true` or a `globs:`
  selector. Keep those `.mdc` files thin — pointers into `ai/*`, not copies.

### Copilot
- `.github/copilot-instructions.md` is a single file with no include mechanism;
  symlink it to `AGENTS.md`. Copilot won't traverse `ai/*` on its own, so the
  inline core block (the non-negotiables) is what it reliably honors. This is why
  the core is generated into `AGENTS.md` rather than linked from it.

### Generic / unknown tool
- If it reads `AGENTS.md`, it gets the contract and can follow the pointers into
  `ai/*` on demand. If it reads nothing automatically, paste `AGENTS.md` into
  its system prompt / rules field.

## Enforcing rules at tool-call time

A rule enforced when a tool call is about to run holds earlier than any git
hook or CI job: before anything is written, sent or merged. Use it where a tool
supports it, for the limits in `ai/00` rule 8 and for the merge gate
(`ai/02` § The merge gate).

- **Claude Code:** `PreToolUse` hooks in `.claude/settings.json` (or a tracked
  `.claude/settings.local.json`), typically on `Bash`. A hook script reads the
  pending call and allows it or denies it with a reason.
- **Other tools:** their equivalent if they have one. Otherwise git hooks and CI
  carry the rule.

Guards worth having:

- **Production and secrets:**
  - deny any command aimed at production;
  - deny writes to a shared environment unless the command carries the user's
    approval marker;
  - deny printing a secret (environment dumps, command tracing, reading a
    secret store to the terminal).
- **Merging:** deny a merge unless the umbrella check succeeded on the PR's
  current head, and the command pins that head.
- **Machine capacity:** refuse heavy commands (the full gate, end-to-end runs)
  while the machine is already saturated.

How to write them:

- **Parse the command properly.** Split on separators, look inside command
  substitutions, and treat heredoc bodies as data unless they are executed. A
  guard that only greps the first word is easy to walk around.
- **Fail closed, on a deadline.** A guard that can't decide in time, or can't
  parse the command, denies it. The capacity guard is the exception: it fails
  open, because blocking all work when it errs costs more than it saves.
- **Test every guard,** with fixtures that must be allowed and fixtures that
  must be denied. The test runner fails if it finds no suites (`ai/11` rule 8).
- **Track the guard and its wiring** in the repository (`ai/05`), so every
  checkout gets them.

## Loading the kit's agents

The definitions in `ai/agents/` carry only `name` and `description`, so any tool
can use them. Any agent can take on one of them by being told to follow the
file. A tool with sub-agent support can also load them as its own:

- **Claude Code:** add a file to `.claude/agents/` with the definition's
  `name` and `description`, plus `model`, `color`, `memory` or `tools` if you
  want them, and a body that loads the kit's file rather than copying it:
  `Read ai/agents/<name>.md in full and act as the agent it defines. Where it and
  this file differ, it wins.` Track the file. A copied body keeps the old rules
  after every update; only the `description` is a copy here, and an amendment
  that changes it says so (`AMENDMENTS.md`).
- **Other tools:** use the tool's own agent or mode directory, if it has one,
  and load the kit's file from it the same way where the tool allows.

## Where tools keep what their agents learn

Known locations, for the `star-pupil`'s discovery step (`ai/agents/star-pupil.md`
§ Phase 1). Layouts change between versions, so these are places to look first,
not guarantees. Paths are for Unix-like systems.

| Tool | Instructions | Memories | Agents and skills | Sessions (deep mode) |
|---|---|---|---|---|
| **Claude Code** | `~/.claude/CLAUDE.md` | `~/.claude/projects/<path-slug>/memory/*.md`; `type: feedback` are the richest. The slug is the project path with `/` replaced. | `~/.claude/agents/`, `~/.claude/skills/` | `~/.claude/projects/<path-slug>/*.jsonl` |
| **OpenCode** | `~/.config/opencode/AGENTS.md` | — | `~/.config/opencode/agent/`, `~/.config/opencode/skills/` | `~/.local/share/opencode/storage/` and its session database; `~/.local/state/opencode/prompt-history.jsonl` |
| **pi** | — | — | `~/.pi/agent/skills/` | `~/.pi/agent/sessions/` |
| **Codex** | `~/.codex/AGENTS.md` | — | — | `~/.codex/sessions/` |
| **Gemini CLI** | `~/.gemini/GEMINI.md` | the "Gemini Added Memories" section of `~/.gemini/GEMINI.md` | — | — |

In a repository, the same tools keep tracked agent definitions and memory under
directories such as `.claude/agents/`, `.claude/agent-memory/` and `.opencode/`.

**Never read** these tools' credential stores, e.g. `~/.pi/agent/auth.json`, or
token and key files under any of these directories.

**Workspace managers** that run several agents side by side, such as herdr, keep
no memories. They do show which agents run in which projects (for herdr:
`herdr agent`, `herdr workspace`).

**Unknown tools:** look in `~/.<tool>`, `~/.config/<tool>`, `~/.local/share/<tool>`
and `~/.local/state/<tool>` for instruction, memory, agent, skill and session
files. When you confirm a new location, add a row here.

## CI-side agents (optional)

If you run an agent in CI (e.g. responding to `@mention` on issues/PRs), keep its
workflow minimal and least-privilege: trigger only on the mention, grant
`contents/issues/pull-requests: read` plus `id-token: write`, add
`actions: read` only if the agent needs to read CI results. The same execution
policy applies — it's the same contract, a different host.
