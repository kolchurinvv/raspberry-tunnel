# Persistent memory framework

A file-based memory system so knowledge survives across sessions. Provider-
agnostic: the same scheme works whether your harness has a built-in memory dir
or you just keep a tracked folder. The governing principle is blunt —
**stale memory is worse than no memory** — so the discipline below (prune,
verify-before-trust) matters as much as the saving.

## Layout

```
<memory-root>/
  MEMORY.md            # the index — one line per memory, always loaded into context
  <slug>.md            # one fact per file, with frontmatter
```

`<memory-root>` is per-scope:

- **Project memory** — tracked in version control, shared with the team. Use for
  anything that should outlive your session and help teammates' agents too.
- **Per-agent memory** — a sub-agent's own folder (see `ai/05-agent-authoring.md`).
- **Personal/global memory** — outside the repo, your harness's user scope.

Tailor the content to the scope: project memory is about *this* project.

## One file = one fact

```markdown
---
name: <short-kebab-case-slug>
description: <one-line summary — used to decide relevance during recall; be specific>
metadata:
  type: user | feedback | project | reference
---

<the fact>
```

For `feedback` and `project` types, structure the body as: the rule/fact first,
then a **Why:** line and a **How to apply:** line. The *why* is what lets
future-you judge whether the memory is still load-bearing.

Link related memories in the body with `[[other-slug]]`. Link liberally — a
`[[slug]]` with no file yet is fine; it marks something worth writing later.

## The four types

- **`user`** — who the user is: role, expertise, durable preferences. ("Deep Go
  background, new to the frontend — frame frontend explanations via backend
  analogues.")
- **`feedback`** — guidance on *how to work*, from both corrections **and
  confirmed successes**. Saving only corrections makes you drift away from
  approaches the user already validated and grow overly cautious. Record the
  why. ("Integration tests must hit a real DB, not mocks — a prior mock/prod
  divergence masked a broken migration.")
- **`project`** — who's doing what, why, by when. Decays fast; **convert
  relative dates to absolute** when saving ("Thursday" → "2026-03-05").
- **`reference`** — pointers to external resources: dashboards, tickets, runbook
  URLs. ("Latency dashboard: grafana.internal/d/api-latency — check it when
  editing request-path code.")

## How to save (two steps)

1. Write the memory to its own `<slug>.md` with the frontmatter above. Check for
   an existing file that already covers it and **update that** rather than
   duplicating.
2. Add a one-line pointer to `MEMORY.md`: `- [Title](slug.md) — one-line hook`.
   `MEMORY.md` is an index, not a memory — no frontmatter, never put memory
   content in it, keep it short (it's loaded into context every session and gets
   truncated past a couple hundred lines). Organize semantically by topic, not
   chronologically.

## What NOT to save

- Code patterns, conventions, architecture, file paths, project structure —
  derivable by reading the current code.
- Git history / who-changed-what — `git log` and `git blame` are authoritative.
- Debugging solutions / fix recipes — the fix is in the code, the *why* is in
  the commit message.
- Anything already in `AGENTS.md` / the provider entrypoint / decision records.
- Ephemeral task state — that's what plans and task lists are for (see below).

These exclusions hold **even when the user says "save this."** If asked to save
a PR list or activity summary, ask what was *surprising* or *non-obvious* about
it — that's the keepable part.

## Recall discipline — stale memory is worse than none

- Access memory when it seems relevant or the user references prior work. You
  MUST access it when the user explicitly says check / recall / remember.
- If the user says to *ignore* memory: don't apply, cite, or compare against it.
- **A memory naming a function, file, or flag is a claim about when it was
  written, not a guarantee it exists now.** Before recommending from it: if it
  names a path, check the file exists; if it names a symbol, grep for it.
  Verify before the user *acts* on it. "The memory says X exists" ≠ "X exists."
- A memory that conflicts with what you observe now: **trust what you observe,
  and update or delete the stale memory** rather than acting on it.
- A memory summarizing repo state (activity log, architecture snapshot) is
  frozen in time. For *current* state, prefer `git log` / reading the code.

## Memory vs. plans vs. tasks

Memory is for what's useful in *future* conversations. For the *current* one:

- Use a **plan** to reach alignment on a non-trivial approach (and update the
  plan when the approach changes — don't save the change as a memory).
- Use **tasks** to break work into steps and track progress within the session.
