# CI discipline

The rule: **a push is not done until every relevant workflow has reached a green
terminal conclusion.** Assuming green because "I didn't change anything that
would break it" is the single most common way red reaches the main branch.

## After every push

1. Identify which workflows fire for this event (push to feature branch vs PR to
   a protected branch vs merge queue usually trigger different sets). Know which
   check is the *required* one (often an umbrella `all-green` job).
2. Watch each one to a terminal state (`success` / `failure` / `cancelled`).
3. Only then report the work as landed.

## Terminal ≠ proven

Reaching a terminal `success` is not the same as the gates having proven
something about **your** commit. Before calling a push done, confirm the run's
head SHA is your tip (concurrency groups cancel superseded runs, and a check mark
can belong to an older commit), enumerate per-job conclusions rather than reading
the top-level badge (an offline runner makes jobs queue and an umbrella resolve
`skipped`, not `failure`), and know which of the gates that "passed" are
warn-only or had no baseline to compare against.

The full enumeration of ways a pipeline goes green without proving anything —
and what to check for each — is in `ai/08-adversarial-review.md` (rule 2). It is
written for reviewing someone else's change; apply the same list to your own.

## One umbrella check

A repository with more than a couple of CI jobs gets one required check that
aggregates them, an "all gates green" job:

- **It depends on every gate job.** Adding a gate means adding it here in the
  same change, just as a new test surface joins the local aggregate (`ai/01`).
- **It passes only if every dependency succeeded.** Cancelled is failure.
  Skipped passes only for jobs on an explicit allow-list per event (say, a
  mobile build on a docs-only change). Any other skip is failure.
- **Tests pin its wiring:** the job's name, its dependency list, and the absence
  of `continue-on-error` or `|| true` on gate steps. A refactor of the workflow
  then can't quietly drop a gate (`ai/11` rule 9).

## The merge gate

- **Merge only on a green head.** The umbrella must have succeeded on the PR's
  *current* head commit. Check the run's head SHA, not the badge (§ Terminal ≠
  proven).
- **Pin that head in the merge command,** so a push that lands in between can't
  merge unchecked (for example `gh pr merge --match-head-commit <sha>`).
- **Where the platform enforces branch protection,** require the umbrella there.
- **Where it doesn't** (some plans don't), enforce the rule twice:
  - a guard on the agent's merge command, where its tools allow one;
  - an audit after every push to the default branch that flags a direct push, a
    merge without a green head, or a force push, and opens an issue for each.

## Use the project's poll recipe — do not hand-roll a loop

If the repo provides a CI-watch recipe (e.g. `just ci-watch`, a
`scripts/ci-watch.sh`), **that recipe IS the poll loop.** It already polls,
diffs step transitions, and exits on terminal state.

- Do **not** wrap it in your own `while`/`until`/`sleep` loop.
- Do **not** substitute ad-hoc `gh run watch` (floods the conversation) or a
  tight `gh run view` poll.
- If your agent harness has a dedicated background/monitor primitive, run the
  watch recipe *under* it as a single verbatim command. Authoring shell
  control-flow to track a run is the signal you've gone wrong (and it burns a
  needless approval round-trip when the recipe was already pre-approved).

If the repo has **no** watch recipe, the minimal correct form is a single
bounded poll on the run's terminal state — not a chatty per-step stream.

## Invoke it the way the repo expects

If the toolchain lives in a managed environment (Nix dev shell, devcontainer,
asdf/mise), invoke the recipe *through* that environment (`nix develop --command
just ci-watch …`), never bypass it — the authenticated `gh`, the helper scripts,
and PATH all come from there. Bypassing gets you "command not found" on a host
that ships none of it by default.

## Timeouts

Cap any watch at a sane ceiling (e.g. ≤30 min for a heavy umbrella whose outer
timeout is 45). If it's still running at the cap, it's wedged — investigate,
don't extend the timeout.

## On failure: pull artifacts, read the focused log first

The umbrella typically uploads per-shard artifacts. Pull them into a repo-local
scratch dir (`.tmp/`), never the system `/tmp`:

```
gh run download <run-id> --name <artifact> --dir .tmp/ci-artifacts/
```

Read the *focused* log (the app's own lines) before the raw firehose. For a
failed end-to-end shard that's usually the screenshot, the UI hierarchy dump,
and the app's own filtered device or browser log.

## Don'ts

- **Don't** retry a failed run with an empty/`--allow-empty` commit to "kick"
  CI. For genuinely flaky infra, `gh run rerun <run-id>`.
- **Don't** rerun a *passing* recipe to "double-check." A flake on a green run is
  signal to read from the existing artifacts, not noise to retry past.
- **Don't** assume a stuck job means a code failure: if a self-hosted runner is
  offline, jobs queue indefinitely and the required check may end up `skipped`
  rather than `failure`. Inspect job status:
  `gh run view <run-id> --jq '.jobs[] | {name, status, conclusion}'`.
