# Authoring sub-agents

How to write a specialized sub-agent (a focused operator the main agent hands a
narrow task to) so it's portable, durable, and consistent across providers.

## Durable agent state lives in version control

**Track agent definitions and their on-disk memory as regular project files.**

Untracked files are per-checkout-on-disk and belong to no branch's history.
Anything that resets, syncs, or cleans a checkout — `git clean -fd`, a post-merge
sync, a fresh worktree — **silently deletes them.** A definition lost this way
is gone for good, unless someone digs it out of git's unreachable objects before
they are pruned.

Concretely:

- Agent definition files: **tracked.** Edits go in their own commit with a
  `chore(agents):` / `feat(agents):` prefix.
- On-disk agent memory (`<agent>/MEMORY.md` + the memory files): **tracked** —
  it's shared institutional knowledge.
- Exception: resolved/stale memories still get **pruned**. The memory
  framework's "stale memory is worse than no memory" rule (`ai/04`) outranks
  "always track" — a resolved finding is deleted, not kept for posterity.
- Transient in-conversation state is **not** memory and does **not** go in git.

## Definition file shape

Keep it portable — a YAML frontmatter block plus a Markdown body. The body is
just a system prompt, so it transfers across providers; only the frontmatter
keys vary (see `ai/providers.md`). Template: `ai/templates/agent-template.md`.

```markdown
---
name: <kebab-name>
description: <when to invoke this agent — write it as triggers + 2–4 concrete
  "Context / user / assistant / commentary" examples so the dispatcher picks it
  for the right requests and not the wrong ones>
# Nothing tool-specific here. Keys like model tier or memory scope are added
# when a tool loads the agent (ai/providers.md).
---

You are <role>. <One-paragraph mission and, crucially, the boundary: what you
hand BACK to the main agent rather than doing yourself.>

## Operating environment
<The repo facts this agent needs: where the source of truth lives, which gates
apply, what it may read vs. write.>

## Modes (if the agent has more than one)
<e.g. build / audit / fix — name them, say which phases each runs, default when
ambiguous, and "ask if intent is unclear.">

## Methodology — follow in order, every time
<Numbered phases. Front-load the cheap high-leverage step (read the existing
system / tokens / conventions BEFORE producing anything). Be explicit that
skipping it is the dominant failure mode.>

## Hard rules
<The non-negotiables specific to this agent's domain.>

## Handoff protocol
<The exact shape of what the agent returns to the main agent, per mode.>

## When to ask vs. when to proceed
<Ambiguity policy.>
```

## Design principles

- **One job, with an explicit boundary.** The agent does its specialty and hands
  back; it does not chase scope it notices in passing (it *records* that for the
  main agent to triage).
- **Read-before-write is a phase, not a footnote.** The highest-leverage step is
  almost always "read the existing tokens / conventions / decisions first."
  State it as Phase 0 and say why skipping it fails.
- **A precise `description` is what makes dispatch work.** Concrete invoke /
  don't-invoke examples beat adjectives.
- **The agent inherits the execution policy.** It still makes incremental
  commits, respects ratchet-only gates, never deletes baselines, etc. — restate
  only the domain-specific deltas, and point at `ai/00`–`ai/03` for the rest.
- **Give it the memory framework** (`ai/04`) and its own memory root if it
  accrues reusable findings. Make "save qualifying findings" a *required* step
  of its handoff, with explicit qualify-or-not criteria, so memory actually
  accumulates instead of being skipped.
