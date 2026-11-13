# AGENTS.md

<!-- ord-ai-n:core:begin -->
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
10. **Agent-authored work gets one adversarial pass before it lands,** and every
    finding gets an answer: fixed, rebutted with evidence, or held for the user's
    ruling. Another round is the user's call: reviewing until the reviewer finds
    nothing doesn't converge. The pass's first question is "is this the change
    that was asked for?", the failure no gate can see, because gates check a
    change against itself. A green pipeline is a claim to verify, not evidence.
    See `ai/08`.

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

Working rules change the same way. When a failure suggests a new rule, propose
it to the user and say where it would live: the Bindings, a decision record,
memory, or a lesson for the kit. A "from now on" in a reply binds nothing, and
no later session will see it. A rule changes when the user rules on it, never on
an agent's say-so.

### `ai/` is the kit's

The installer writes `ai/`, and every update replaces it. Never add, edit,
rename or delete a file there, and never edit `ai/.kit-manifest` to make the
check pass: an update may overwrite or delete whatever the kit didn't ship. This
repository's own docs (runbooks, specs, test plans, checklists) go where
`<project-docs>` says, named the way this repository names things rather than
after the kit's numbered modules. See `ai/06`.
<!-- ord-ai-n:core:end -->

## Bindings

| Binding | Here |
|---|---|
| `<gate-command>` | `nix build '.#checks.x86_64-linux.tunnel' -L --no-link`. Boots the 8 VMs in `tests/tunnel.nix`, using about 8 GB for about seven minutes. Needs x86_64 Linux with KVM and no secrets. A flake in a git repository only sees tracked files, so `git add` new files first. This machine also hosts other CI, and a parallel run here has already caused an OOM that killed CI runners. So run one at a time, sub-agents included, under the box's shared lock, as `private/gate.md` describes, and only when no CI job is running and at least 12 GiB is free. Where it can't run, say so and report the work as unverified. Gap: it covers the routing and firewall roles on x86 only, and nothing evaluates or builds the ARM Pi images. |
| `<base>` branch | `main`. The repository has no remote, which the user doesn't need for now. |
| Branches and commits | Commit directly to `main`, the user's decision on 2026-11-01, which overrides `ai/01` § Branching. Conventional Commits, one concern per commit, with a body saying what changed and why. |
| Decision records | `docs/decisions/D<n>-*.md`, from `ai/templates/decision-record.md`. Only D1 so far. Earlier decisions came from the user in conversation and aren't written down. |
| Status docs (rule 5) | `README.md`, including its status table and first-time setup. |
| Generated baselines (rule 3) | `flake.lock`, the only one. Update it only when the user asks, as its own commit. |
| `<read-back>` (`ai/10`) | none: no application data store. The power-cut subtest in `tests/tunnel.nix` shows the Tailscale state on disk survives a crash, by checking the tunnel returns unaided. |
| `<floors>` (`ai/11`) | none: this is NixOS configuration and no coverage is measured. |
| `<exemptions>` (`ai/11`) | none: no gate takes exemptions. |
| `<static-checks>` (`ai/12`) | The gate evaluates the NixOS modules and runs NixOS's nftables ruleset check while building the VMs. `modules/pi-hardware.nix` refuses to build a Pi system or image while the `admin-key` or `extra-local-nets` flake input is still its placeholder, so each build has to be given its Pi's key and its site's ranges with `--override-input`. Gaps: none: no lint or format gate yet (no `nix fmt`, `statix` or `deadnix`). |
| Quality gates (rule 4) | The subtests in `tests/tunnel.nix` may only get stricter, and so may the forward chains in `hosts/`, Pi B's bridge table `tv-port`, and the block lists in `modules/local-nets.nix` and `private/extra-local-nets.nix`. |
| Private data | `private/`, gitignored: this site's real network values and machine notes, which public files leave out and may point to. Never commit its contents or copy them into tracked files. |
| CI (rule 6) | none: no remote and no CI. The gate only runs locally. |
| Adversarial review (core 10) | A fresh sub-agent follows `ai/agents/adversary.md`, given the request and the diff, and told the gate's one-at-a-time limit. No repository-specific review tool. |
| Agent definitions | `ai/agents/*.md`, from the kit. The repository has no agents of its own. |
| Memory root | Claude Code's per-project memory directory, outside the repository and untracked. The user's decision on 2026-11-01, which overrides core rule 9: it holds network details and the repository may be published later. |
| Autonomy | Agents edit this repository's working tree, run the gate, and commit to `main` at each coherent stopping point without asking. That's the user's decision on 2026-11-02, which overrides their global "commit or push only when asked" for this repository. Pushes and writes outside this repository (such as `~/.config/nixos`) need the user to ask. |
| `Feedback (star-pupil)` | off |
| The kit itself | Vendored from ord-ai-n (version in `ai/.kit-manifest`). Update by re-running the installer; never edit `ai/` or the block above. |
