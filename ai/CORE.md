## Agent working contract

<!-- Generated from ai/CORE.md by ai/sync.sh — do not edit this block in place;
     the next sync overwrites it. Repo-specific values belong in Bindings. -->

How AI agents work in this repository, whatever the tool or model. This block is
the non-negotiable core; the depth lives in the `ai/` modules — read the relevant
one before doing the work it governs. `<angle-bracket>` placeholders are bound
once, in this file's **Bindings** section. Where the Bindings or this
repository's decision records say otherwise, they win over the kit's defaults.

### The modules

| When you are… | Read |
|---|---|
| Operating at all (start here) | `ai/00-execution-policy.md` |
| Committing, branching, opening a PR, tagging | `ai/01-commit-and-pr-hygiene.md` |
| After any push / watching CI | `ai/02-ci-discipline.md` |
| Writing code that touches the network, storage, or native/external APIs, or that handles errors at all | `ai/03-defensive-coding.md` |
| Recording or recalling cross-session knowledge | `ai/04-memory-framework.md` |
| Authoring or editing a sub-agent definition | `ai/05-agent-authoring.md` |
| Deciding *where* a piece of knowledge belongs | `ai/06-knowledge-placement.md` |
| Deciding which specialist to delegate to | `ai/07-agent-roster.md` (+ `ai/agents/*.md`) |
| Reviewing agent-authored work before it lands | `ai/08-adversarial-review.md` (+ `ai/agents/adversary.md`) |
| Running several agents on one codebase at once | `ai/09-parallel-streams.md` |
| Writing tests, especially for anything that persists data | `ai/10-test-discipline.md` |
| Setting or changing a coverage floor, threshold, exemption or grandfather list | `ai/11-quality-ratchets.md` |
| Configuring the type checker, linter, formatter or a guard script; writing an architecture rule | `ai/12-static-strictness.md` |

### Non-negotiable core

These are the rules that change turn-to-turn behavior. The modules expand each.

1. **Incremental, focused commits.** A sequence of small commits that each leave
   the tree buildable — never one large end-of-session dump.
2. **Start from a known-good state.** Run the gate (`<gate-command>`, see
   `ai/01`) before changing anything. If it's red at session start, surface that
   to the user *before* touching code — never fix-forward silently. If your
   environment cannot run it, say so up front and in your report — never present
   unverified work as verified.
3. **Generated baselines are append-only.** Golden snapshots, fixtures,
   recorded outputs, lockfiles-of-record: never `rm`, never overwrite, never
   fold into a "cleanup" pass. Regenerate only on explicit user approval,
   recorded in the commit message.
4. **Quality gates ratchet upward only.** Coverage floors, lint thresholds, and
   checks may be raised, never relaxed — except with explicit user approval in
   the commit message. See `ai/11`.
5. **Keep the human-facing status doc aligned in the same change.** When work
   changes project shape (a milestone advances, a component flips status),
   update the status doc (README / roadmap) in the *same* commit series.
6. **Watch every push to a green conclusion.** "It probably didn't break
   anything" is how red reaches the main branch. See `ai/02`.
7. **Fail soft on the world, loud on yourself.** Networks time out, payloads are
   malformed, storage is unwritable: degrade gracefully. But a broken invariant,
   a contract your own code violates, or a check that cannot run fails loudly and
   at once — never a silent default, a swallowed error, or a vacuous pass. See
   `ai/03`.
8. **Autonomous mode is conditional on rules 1–7.** Where a repository grants
   broad tool permissions, they hold only while the rules above are honored. The
   instant one cannot be, drop out and confirm with the user. See `ai/00`.
9. **Durable agent state lives in version control.** Agent definitions and
   on-disk memory are tracked files, not per-checkout scratch — anything that
   cleans or re-syncs a checkout will otherwise silently delete them. See
   `ai/05`.
10. **Agent-authored work gets an adversarial pass before it lands,** and every
    finding gets an answer: fixed, rebutted with evidence, or held for the user's
    ruling. The pass's first question is "is this the change that was asked
    for?", the failure no gate can see, because gates check a change against
    itself. A green pipeline is a claim to verify, not evidence. See `ai/08`.

> **Numbering is a contract.** Items 1–8 mirror `ai/00-execution-policy.md`'s
> rule numbers exactly, so "rule 4" means the same thing in every repo and every
> module that cites it. Items 9–10 are core rules whose depth lives elsewhere.
> If you change one list, change the other in the same commit.

**Done means done.** A change is finished only when `ai/00` § Definition of
done holds. Name whatever you couldn't verify in your report.

### Autonomy level

The default posture is **propose-then-act for anything outward-facing or hard to
reverse** (pushing, opening PRs, deleting, anything that leaves the machine).

A repository MAY grant a broader **autonomous ("yolo") mode** — agents operate
with wide tool permissions and commit/push without per-step confirmation —
**conditional on the execution policy (rules 1–7) being honored**. The instant
those rules cannot be followed, drop out of autonomous mode and confirm with the
user. Grant and scope this in the provider config (`ai/providers.md`), and record
what is granted in Bindings.

### Sources of truth

Stack and architectural decisions come from exactly two places: **the user**, or
the repository's **decision records** (e.g. `docs/decisions/*`, ADRs — Bindings
names this repository's). Nothing else — do not infer choices from sibling repos,
prior conventions, or training priors. If the decision records are silent, ask
the user. This block governs *how* you work; the decision records govern *what*
you build.
