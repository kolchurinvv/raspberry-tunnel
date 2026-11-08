# AI workflow kit

A tool- and model-agnostic set of rules and scaffolding that gives any AI-enabled repo
the same robust agent behavior: no drift, clean incremental commits, disciplined
CI, durable memory, and automation that removes the tedium of committing,
pushing, and gating by hand.

It drops into any repository and works with any coding agent, harness or model.

## What's here

| File | Concern |
|---|---|
| `../AGENTS.md` | Provider-neutral entrypoint: the core block (generated from `CORE.md`) + this repo's Bindings. |
| `CORE.md` | The non-negotiable core and module index: the block `sync.sh` generates into every adopting repo's `AGENTS.md`. |
| `sync.sh` | Vendors the kit into a repo (`sync.sh <repo>`); verifies a vendored copy (`sync.sh --check <repo>`). |
| `../install.sh` | The `curl \| bash` entry point: downloads the kit at one commit and runs its `sync.sh` on the current repo. |
| `INSTALL.md` | Step by step: install into an existing repo, fill in the Bindings, update, remove, send lessons back; or a prompt that hands the adoption to an agent. |
| `00-execution-policy.md` | How agents operate: incremental commits, known-good start, append-only baselines, ratchet-only gates, conditional autonomy. |
| `01-commit-and-pr-hygiene.md` | The gate, commit messages (host convention first), branching/worktrees, PRs, milestone tags. |
| `02-ci-discipline.md` | Watch every push to green; one umbrella check; merge only on a green, pinned head; use the poll recipe; artifact triage. |
| `03-defensive-coding.md` | Fail soft on the world: the six boundaries. Fail loud on yourself: no swallowed errors, no defaults for impossible absences, exhaustive handling. |
| `04-memory-framework.md` | File-based cross-session memory; stale memory is worse than none. |
| `05-agent-authoring.md` | Writing sub-agents; keeping definitions + memory in git. |
| `06-knowledge-placement.md` | The pointer pattern: where each kind of knowledge belongs. |
| `07-agent-roster.md` | The standing roster of specialists and the delegation policy. |
| `08-adversarial-review.md` | Reviewing agent-authored work: conformance-to-request first, green is a claim, falsify your own findings; the review-fix loop that answers every finding. |
| `09-parallel-streams.md` | Running several agents on one codebase: frozen/owned/shared allocation, append protocol, resource serialization. |
| `10-test-discipline.md` | What tests must prove: persistence verified by reading it back from the real store; tests that can fail; real dependencies with fakes quarantined; behaviors covered, not only lines; test first, by layer. |
| `11-quality-ratchets.md` | The machinery behind rule 4: floors per area raised only, the floor gate as the local gate, one exemption registry per family of gates, grandfather lists that only shrink, gates that fail loud, guards on the gates themselves. |
| `12-static-strictness.md` | Type checker, linter and formatter as hard gates (warnings are errors), strictest settings, architecture rules as lint rules, invariant guard scripts, drift checks for generated code. |
| `agents/*.md` | Drop-in specialist definitions: adversary, coding-teacher, explorer, planner, reviewer, star-pupil. |
| `providers.md` | How each AI tool ingests the kit; one source of truth, thin entrypoints. |
| `templates/pre-commit` | Hook that delegates to the project gate. |
| `templates/agent-template.md` | Provider-neutral sub-agent definition skeleton. |
| `templates/claude.settings.json` | Adapter for one tool: an example yolo-but-guarded permissions profile. |
| `templates/gate.just` | The `check` / `precommit` / `hook-install` / `ci-watch` recipe contract. |
| `templates/decision-record.md` | A decision record: context, decision, consequences, and the check that enforces it. |

## Adopt it in a repository

The kit travels as a **vendored, read-only `ai/` directory** plus **one generated
block in the host's `AGENTS.md`**. Everything repo-specific lives in the host's
own files, never under `ai/`, so a re-sync is a plain copy and its diff is
exactly the kit's change.

Nobody adopting the kit can promise which harness or model will read it, so it
takes hold in three layers, from weakest guarantee to strongest:

1. **The inline core.** The ten rules, generated into `AGENTS.md`, which nearly
   every coding agent loads unprompted. A model that follows no pointer still
   reads them.
2. **The modules.** `ai/*.md` hold the depth, for agents that follow the core's
   pointers.
3. **Mechanical enforcement.** Hooks and CI checks bind whatever wrote the
   change, including an agent that read nothing. A rule that matters and that a
   script can check should be checked by one.

### How

Step by step, including the Bindings, updating and removing: `INSTALL.md`. The installer ships `ai/` and nothing else. The kit
repository's `lessons/` is an inbox of candidate rules: a lesson reaches an
adopting repository only once it has been promoted into an `ai/` module.

Two rules shape every step:

- **Reconcile, don't stack.** Where the host already has rules (a constitution,
  a commit convention, its own review skill), the host's documents win over the
  kit's defaults. Bindings say so, and the host's existing tools point at the
  matching module instead of a parallel one.
- **Never edit the kit in place.** Every update overwrites `ai/` and the block,
  and `sync.sh --check` fails on in-place edits. Repo-specific values go in
  Bindings; changes to the kit itself go to ord-ai-n.

The kit must live at `ai/` in the host, because its modules cite each other by
that path. When developing the kit itself, run `ai/sync.sh <host-repo>` from a
checkout instead of the installer.

## Design stance

- **Content is tool- and model-agnostic; adapters are thin and fenced off.**
  No rule, module or agent may depend on a particular coding tool, harness or
  model, or name one. What each tool needs in order to load the contract
  (entrypoint files, import syntax, settings, where it stores memories) lives
  only in `providers.md` and `templates/`. Never fork the rules per tool: point
  every tool at the same `AGENTS.md` + `ai/*`.
- **The kit carries no trace of where its knowledge came from.** No project,
  product, client, person, incident, figure or prompt from the material it was
  compiled from appears anywhere in this repository: not in the rules, the
  examples, the lessons or the commit messages. A rule states the behavior and
  the general failure it prevents. Evidence is weighed during review and then
  left behind; it is never stored.
- **The instruction file is an index, not a junk drawer.** Decisions go in
  decision records, procedures in runbooks, history in commit messages, durable
  facts in memory — the entrypoint gets pointers (`06-knowledge-placement.md`).
- **Autonomy removes tedium, not judgment.** Broad permissions to commit/push/
  test freely, conditional on the execution policy; hard stops on the few
  genuinely dangerous actions.
