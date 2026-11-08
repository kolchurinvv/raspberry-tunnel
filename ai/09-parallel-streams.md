# Parallel streams

How to run several agents on the same codebase at once without their work
colliding. The prerequisite is not coordination-in-the-moment — it is a **written
allocation, agreed before any stream starts**, that makes most collisions
structurally impossible and makes the rest auto-mergeable.

Without one, parallel agents produce the same three failures every time:
conflicting edits to the handful of files every feature must touch, two streams
independently inventing the same primitive, and both wedging on a shared local
resource that only one can hold.

## Freeze the foundation first

Parallel streams only work when they build on something that is not moving. Land
the shared foundation — shell, routing, identity, permissions, the dispatch
tables — and **freeze it** before forking work. A stream that has to change the
foundation is not a parallel stream; it is a serial one wearing a disguise, and
it should be resequenced ahead of the others rather than allowed to edit under
them.

## The three tiers

Classify every file in the blast radius of the planned work into exactly one
tier, and write the classification into a contract document under your decision
records.

| Tier | Rule | What goes here |
|---|---|---|
| **FROZEN** | Never edited by any stream. A needed change stops the stream and goes to the user. | The foundation: app shell, routing entry points, dispatch tables, auth/permission modules, the invariant tests that pin them. |
| **OWNED** | Exactly one stream may edit; that stream may build freely. | Each stream's own feature directories, its own data-layer module, its own schema/proto and generated output, its own tests and E2E flows. **The allocation must be disjoint** — if two streams need the same file, it belongs in FROZEN or the split is wrong. |
| **SHARED — append-only** | Every stream edits, under the append protocol below. | The handful of registry-shaped files every feature must touch: message/i18n catalogs, coverage config, CI shard registration, the task-runner file, the status doc. |

## The append protocol

Shared files are only safe because of *how* they are edited. Git merges
non-overlapping line insertions cleanly; it does not merge two rewrites of the
same block.

- **Append-only.** Add rows; never reorder, never reformat, never "tidy while I'm
  here." A stream that re-sorts a shared file conflicts with every other stream
  in flight.
- **Deterministic position.** Where the file is a list, insert alphabetically (or
  by whatever total order the file already uses), so two streams adding different
  rows land in different places.
- **Label every edit** with its stream identifier in a comment. When a conflict
  does happen, the label is what makes the resolution obvious instead of archaeological.
- **Reserve namespaces.** For catalog-shaped files — messages, telemetry events,
  feature flags, route names — give each stream a key prefix it owns, and forbid
  writing into another stream's prefix. This turns the single highest-conflict
  file in most repos into a non-issue.
- **Respect the type source.** Where one catalog defines the type the others must
  satisfy, that one is edited first, in the same commit; otherwise every sibling
  catalog becomes a type error for every other stream simultaneously.
- **Approval-gated rows stay approval-gated.** If a shared file contains an
  exemption registry or a parity waiver, a stream proposes the entry in its
  handoff and does not add it (`ai/00-execution-policy.md` rule 4).

**The same-change rule:** when a stream lands a real surface, it removes that
surface's placeholder exemption *and* adds its gate entry in the same change. A
stale exemption should fail the gate, so splitting these across commits breaks
everyone else's build, not just the author's.

## Verify before you assume "consume"

The foundation contract says what exists. It is written before the streams run,
and it drifts. **The spec and the design source outrank what is currently built.**

Before a stream treats a foundation capability as ready to consume, it audits
that capability against the spec — and assumes it must **extend** it until
verified otherwise. Streams that skip this discover mid-implementation that the
thing they were told to consume is a stub, and either block or silently build on
sand.

## Shared local resources: serialize, and surface

Some resources cannot be shared no matter how the files are allocated — a single
device emulator, a GPU, a CI pipeline that holds a global lock, a test database, a
staging deployment. These are the failures that waste the most wall-clock,
because both streams appear to be working right up until both fail.

- **Name them in the contract**, with their concurrency limit.
- **Gates are the usual way in.** A test runner that recreates a fixed-name
  container or database deletes the instance another checkout is using. Name
  such runners next to the resource they take.
- **Everything that does not need them runs freely in parallel** — typecheck,
  lint, unit tests, the offline half of the gate. Do not serialize work that has
  no contention.
- **Before touching one, the stream stops and surfaces to the user for a
  go-ahead.** This is a deliberate exception to autonomous operation
  (`ai/00-execution-policy.md` rule 8): the agent cannot see the other streams,
  so the human is the scheduler. An agent that assumes the emulator is free is
  the agent that corrupts another stream's test run.
- **A resource-contention failure looks like a flake.** Budget for the confusion:
  when a stream's E2E goes red while another stream held the device, the finding
  is scheduling, not code. Record it as such rather than chasing a ghost.

## One folder, one branch

Parallel streams pair naturally with a worktree-per-branch layout (see
`ai/01-commit-and-pr-hygiene.md`): each stream gets its own folder for that
branch's entire lifetime, so no stream ever switches branches under another's
feet. Never `checkout -b` inside an existing worktree.

## Write the contract down

The allocation is a decision record, not a conversation. It belongs in
`docs/decisions/*` (per `ai/06-knowledge-placement.md`) and it must name, at
minimum:

1. The frozen set, explicitly enumerated.
2. The owned-file map, one row per stream, verifiably disjoint.
3. The shared-append list, with the protocol and any reserved namespaces.
4. The shared local resources and their concurrency limits.
5. Where each stream's own execution plan lives.

Hand every stream the same document. A contract that only the orchestrator has
read is not a contract.
