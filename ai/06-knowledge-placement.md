# Knowledge placement (the pointer pattern)

The instruction file is not a junk drawer. Every durable fact has a *correct*
home, and the entrypoint (`AGENTS.md`) gets a **pointer** to it —
not a copy. This is what keeps the entrypoint small enough to actually be read
every session.

## Where each kind of knowledge lives

| Kind of knowledge | Home |
|---|---|
| A **decision** (stack choice, architectural invariant, "we use X not Y") | A decision record: `docs/decisions/*`, ADRs, a dated `stack-decisions.md`. Dated, append-mostly, supersession noted. Template: `ai/templates/decision-record.md`; its Enforcement section names what holds the decision. |
| A **procedure** (how to run/deploy/regenerate/test something by hand) | A runbook, in `<project-docs>` (Bindings), for example `docs/runbooks/*`. |
| A **spec, test plan, checklist or reference** for this project | `<project-docs>`, for example `docs/`. |
| **History / why a change was made** | The commit message. Not a doc, not the entrypoint. |
| **In-flight task state** | A task list / plan in the current session — not a file. |
| **Cross-session institutional knowledge** (user prefs, validated approaches) | The memory framework (`ai/04`). |
| A **rule that changes turn-to-turn agent behavior** | The entrypoint — but only as a pointer, and only if it meets the bar below. |

**Never under `ai/`.** That directory is the kit's: the installer writes it, and
every update replaces it. A project file placed there fails `ai/sync.sh --check`.
Listing it in `ai/.kit-manifest` to quiet the check doesn't make it the kit's,
and an update may delete it. Name project files the way the project names
things, not after the kit's numbered modules: a project `ai/11-…` and the kit's
`ai/11` would be two files with one name in every citation.

## The bar for putting something in the entrypoint

A rule earns a line in `AGENTS.md` only if **both**:

1. it changes how the agent behaves *turn to turn* (not just background
   context), **and**
2. getting it wrong has caused — or would obviously cause — real damage.

Everything else is a pointer to the decision record, runbook, or commit history
where it actually lives. Background "what this app is" context belongs in the
entrypoint only in cheat-sheet form, with the authoritative detail pointed-to.

## Why this matters

- An entrypoint that accretes every fact becomes one nobody reads, and the
  load-bearing rules drown.
- Decisions in dated records can be *superseded* cleanly (D24 supersedes D20);
  decisions copied into prose silently contradict each other.
- History in commit messages is queryable (`git log`, `git blame`) and attached
  to the exact change; history in a doc rots.

When you learn something durable, ask "what's the *correct* home?" before
defaulting to the entrypoint. Usually it's a decision record, a runbook, a
commit message, or a memory — and the entrypoint gets at most a pointer.

## Promoting a lesson out of a project

Knowledge learned in one project lives first where that project's agents keep it
(memories, instruction files, review findings, corrections in a transcript).
Some of it is about *how agents should work anywhere*, and belongs in the kit so
every adopting repository gets it. The `star-pupil` agent
(`ai/agents/star-pupil.md`) harvests candidates; this is the bar they must clear.

A lesson moves into the kit only if **all three** hold:

1. **Portable.** It would hold in a repository with a different stack, team, and
   product. Rewrite it without the project's names; if it stops making sense, it
   is project-local.
2. **Evidenced.** It came from something that happened: a failure it would have
   prevented, a correction the user made, or an approach the user confirmed.
   Independent sources (two projects, two tools) make it stronger. The evidence
   is weighed when the lesson is reviewed and is never written into the kit:
   neither the lesson nor the commit that promotes it names where it came from
   (`README.md` § Design stance).
3. **Behavioral.** It changes what an agent does turn to turn. Background facts
   don't qualify.

Each candidate lands in exactly one place:

| It is… | Home |
|---|---|
| Portable, evidenced, behavioral | The kit: a module rule (or the core, if it clears the entrypoint bar above), plus a Bindings row wherever a host must supply a value. |
| About the person, not the work (how terse to be, how to report) | The user's personal profile, installed into each tool's global instruction file. Never a shared repository. |
| True only of one project | That project: its decision records, runbooks, or Bindings. |
| Already in the kit | Nowhere new. It is evidence for the existing rule; retire the source copy. |

After promotion, the source copy is retired (deleted, or reduced to a pointer at
the kit rule). Two live copies drift, and stale memory is worse than none
(`ai/04`).
