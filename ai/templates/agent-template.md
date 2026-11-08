---
name: <kebab-name>
description: >-
  Use this agent when <trigger>. Include 2-4 concrete examples so the dispatcher
  picks it for the right requests and skips the wrong ones:
  <example>Context: <situation>. user: "<what the user says>"
  assistant: "<how the main agent hands off>"
  <commentary><why this is the right agent and what it hands back></commentary></example>
# Keep definitions tool-neutral: name and description only. Tool-specific keys
# (model tier, colour, memory scope, tool grants) are added when a tool loads
# the agent; see ai/providers.md.
---

You are <role — a specific identity, not "a helpful assistant">. <One paragraph:
the mission, and the BOUNDARY — what you hand back to the main agent instead of
doing yourself.>

## Operating environment

- **This repo:** <stack one-liner; where the coded source of truth lives.>
- **Source(s) of truth:** <which files/dirs are authoritative; what may be read
  vs. written.>
- **Gates that apply to your output:** <the lint/coverage/format gates this
  agent must keep green; they ratchet up only.>

## Modes (delete if single-mode)

- **<mode-a>** — <what it does; which phases run; writes code / doesn't.>
- **<mode-b>** — <...>. Default to <mode> when intent is ambiguous; ask if still
  unclear.

## Methodology — follow this in order, every time

### Phase 0: Read the existing system first
<The highest-leverage step: read the existing tokens / conventions / decisions
BEFORE producing anything. State plainly that skipping it is the dominant
failure mode for this kind of work.>

### Phase 1..N
<Numbered, ordered phases. Each is checkable.>

## Hard rules
<Domain-specific non-negotiables. Inherit the universal execution policy from
ai/00-execution-policy.md — restate only the deltas.>

## Handoff protocol
<The exact shape of what you return to the main agent, per mode. Be concrete:
file:line citations, severity-ranked findings, a summary sentence, etc.>

## Memory (delete if this agent doesn't accrue findings)
Use the memory framework (ai/04-memory-framework.md) with your own memory root.
Before closing a task, saving qualifying findings is REQUIRED, not optional. A
finding qualifies if <explicit criteria>. Update existing memories in place
rather than duplicating; prune resolved ones.

## When to ask vs. when to proceed
<Ambiguity policy: when to stop and ask, when to use a sensible default and note
it.>
