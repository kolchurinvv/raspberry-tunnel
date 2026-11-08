# Execution policy

How agents operate, independent of language or stack. Rules 1–7 are
unconditional. Rule 8 (autonomous mode) is *conditional* on the others being
honored. These are the rules that prevent drift and laziness; treat them as
load-bearing, not advisory.

> Placeholders in `<angle-brackets>` are repo-specific — bind them once, in the
> Bindings section of the repository's `AGENTS.md`. The canonical
> one is `<gate-command>`: the single command that runs the full local check
> aggregate (see `ai/01-commit-and-pr-hygiene.md`).

## 1. Incremental commits

Land work as a sequence of small, focused commits, not one large dump. Each
commit:

- leaves the tree in a buildable / passing state,
- describes exactly one logical change,
- is something a reviewer could read in isolation.

A long agent session that ends with a single 40-file commit has failed this
rule even if the code is correct. Commit at every coherent stopping point.

## 2. Start every session from a known-good state

Run `<gate-command>` at the start of a session **before** making changes —
unless the checkout demonstrably already matches a known-good baseline (e.g. you
just verified it, or it's at a tagged milestone).

If the gate is **red at session start**, surface that to the user before doing
anything else. Do not "fix forward" silently — a pre-existing failure is the
user's to know about, and silently absorbing it into your change destroys the
signal of what *you* broke.

If your environment **cannot run the gate** — no container runtime, no network,
a sandbox missing the toolchain — say so before you start and again in your
report. You may still do the work, but it is unverified, and it must be
presented as unverified: never let "I couldn't run it" read as "it passed." In
a repository whose agents run in harnesses nobody controls, this is the case
you will meet most often.

## 3. Never delete generated baselines

Golden snapshots, recorded fixtures, approval-test outputs, committed codegen,
and lockfiles-of-record are **append-only from an agent's perspective**.

- Never `rm` one.
- Never overwrite one without explicit confirmation.
- Never sweep one into a "cleanup" or "regenerate everything" pass.

Regenerate **only** when the user explicitly approves it, and record that
approval in the commit message (e.g. `chore(goldens): regenerate onboarding
snapshots (user-approved)`). The baseline is the test oracle; silently rewriting
it is silently deleting the test.

**Admit a new baseline only when it is stable.** Capture it in two independent
runs and admit only what both agree on. A screenshot that differs between two
runs of the same commit is noise, and admitting it makes the gate flaky.
Tooling that writes baselines dry-runs by default, and archives the old version
before replacing it. A missing baseline makes a comparison pass by comparing
nothing. That is a gap to fill, not a pass (`ai/11` rule 8).

## 4. Quality gates ratchet upward only

Coverage floors, lint thresholds, type strictness, and check counts may be
**raised**, never **lowered**. The one exception is explicit user approval
recorded in the commit message. A red gate is fixed by fixing the code, not by
lowering the bar. Disabling a check "temporarily" is the same violation.

No inline escape hatches for gates that have a central exemption mechanism: if
the project has an exemptions registry, exemptions live *only* there, edits to
it require user approval, and stale entries (matching nothing) should fail the
gate. Scattered `// lint-allow` comments rot; a central registry is auditable.

The machinery (floors per area, the floor gate as the local gate, exemption
registries, grandfather lists that only shrink) is in
`ai/11-quality-ratchets.md`.

## 5. Keep the human-facing status doc aligned

The status doc (top-level `README.md`, a roadmap board, a `STATUS.md`) is the
manager-facing dashboard. When work changes the *shape* of the project — a
component advances a maturity level, a release ships or starts, an infra slot
flips status — update it in the **same** change. Per-commit trivia does not need
to touch it; release-shape progress does.

## 6. Watch every push to green

After any push, watch every relevant CI workflow to a terminal conclusion before
considering the work done. The full discipline is in `ai/02-ci-discipline.md`.
The one-line version: **assuming green is how red reaches the main branch.**

## 7. Fail soft on the world, loud on yourself

Code you write must not crash on external, upstream or system failure: degrade
gracefully. And it must never hide its own failures: a broken invariant, a
violated contract, or a check that cannot run fails loudly, at once, where the
cause is. A silent fallback around your own code turns the bug you just
introduced into one nobody will find. Full rules in
`ai/03-defensive-coding.md`; for gates, `ai/11-quality-ratchets.md` rule 8.

## 8. Autonomous ("yolo") mode — conditional

Where a repository grants it (in provider config), agents may operate with broad
tool permissions: read freely, edit, commit, and push without per-step
confirmation, to move fast without tedious manual gating.

This permission is **conditional on rules 1–7 above.** If any of them cannot be
honored — the gate is red and you can't explain why, you're about to delete a
baseline, a change can't be made incrementally — **drop out of autonomous mode
and confirm with the user before proceeding.**

Autonomous mode never extends to:

- **Writes outside this repository.** Reads from sibling repos may be permitted;
  writes to them always require explicit user approval.
- **Irreversible outward actions** beyond normal commit/push — force-pushing
  shared branches, deleting remote branches/tags, publishing releases, touching
  production — unless the user durably authorized that specific action.
- **Production and shared environments.** Never run a command against
  production. Writes to a shared environment (staging, QA, a shared database)
  need the user's approval for that action.
- **Revealing secrets.** Never print, echo, log or dump a secret's value. That
  covers environment dumps, tracing modes that echo commands, and reading a
  secret store to the terminal. Use secrets by reference only.

**A denied action is a stop.** When a guard, a permission rule or the user
denies an action, stop and report it. Never rephrase the command, split it, or
route it through another tool to get the same effect. Some guards accept an
approval marker, such as an environment prefix or a flag. That marker records
that the user approved this specific action; it is not a way for an agent to
authorize itself. Where your tools can guard tool calls, use that for the limits
above (`ai/providers.md` § Enforcing rules at tool-call time).

Autonomy is about removing tedium (manual commit/push/test cycles), not about
removing judgment on the few actions that are actually dangerous.

## Definition of done

A change is done when all of these hold, and the report says which were
checked and how:

1. **It does what was asked,** every part of it (`ai/08` rule 1).
2. **Its tests are in the same commit series,** written test-first where the
   layer calls for it, with persistence read back from the real store
   (`ai/10`).
3. **The fast tier passed on the final head,** and the strict tier too where
   the repository requires it before a PR (`ai/01` § The gate).
4. **No gate was loosened** without the user's approval recorded in the commit
   (rule 4, `ai/11`).
5. **The review-fix loop is closed:** every finding fixed, rebutted with
   evidence, or held for a ruling (`ai/08`).
6. **The status docs are current** (rule 5).
7. **CI is green on the pushed head,** every job, watched to the end (rule 6).

Whatever couldn't be done or verified is named in the report, never left to be
inferred from silence.
