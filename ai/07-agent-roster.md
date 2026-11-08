# Agent roster & delegation

The main agent is a generalist. Most robust work happens when it **delegates
narrow, well-scoped work to specialists** and keeps only the conclusion. This
module is the catalog of standard specialists plus the policy for when to reach
for each. Authoring a *new* specialist is `ai/05-agent-authoring.md`; this is
about the standing roster every repo should have.

The roster is tool-agnostic. Some agents map to a tool's built-ins (many ship a
general-purpose agent, a search agent and a planner); where a tool lacks one, the
definition in `ai/agents/*.md` provides the same behavior. Any agent can take on
one of these roles by being told to follow its file. A tool that supports
sub-agents can also load the files as its own (`ai/providers.md`).

## The standing roster

| Agent | Use it when | Does NOT |
|---|---|---|
| **explorer** (`ai/agents/explorer.md`) | You need to locate code across many files/dirs and only want the conclusion, not the file dumps. Broad fan-out search. | Review, audit, or judge code quality; read whole files when an excerpt suffices. |
| **planner** (`ai/agents/planner.md`) | A task needs an implementation strategy before any code is written — critical files, sequencing, trade-offs. | Write or edit code. |
| **coding-teacher** (`ai/agents/coding-teacher.md`) | The user is *learning* — "how do I…", wants a concept or some code explained, or is stuck and wants understanding, not just a patch. | Use when the user just wants the work done — that's a straight implementation. |
| **reviewer** (`ai/agents/reviewer.md`) | A diff needs a correctness/quality pass with adversarial verification before it lands. | Rubber-stamp; widen scope beyond the diff. |
| **adversary** (`ai/agents/adversary.md`) | A change an *agent* authored from a spec/ticket it interpreted alone is up for merge, and it touches a shared layer, an invariant, a permission seam, or spec'd behaviour. Assumes the change is wrong and checks conformance-to-request first. | Review human-authored diffs (use **reviewer**); edit, fix, merge, or approve anything. |
| **star-pupil** (`ai/agents/star-pupil.md`) | Knowledge agents picked up while working (corrections, confirmed approaches, gotchas) should outlive the project or tool it was learned in: at a milestone, before archiving a project, or when the user says a lesson should apply everywhere. Reads every agent tool, not just one. Ships to every adopting repo as the kit's feedback loop: opt-in through the repo's Bindings, and it prints what it will and won't collect before reading anything. | Read beyond its disclosed scope, send anything without approval of the exact text, edit or delete a source, or promote into the kit's modules. |
| **general-purpose** | A multi-step research/search task that doesn't fit a specialist, or you're not confident a search lands in the first few tries. | Replace a specialist when one fits. |
| **domain specialists** (e.g. a `design-replicator`) | A repo-specific, high-fidelity task with its own methodology. Author per `ai/05`. | — |

Host-specific helpers that don't generalize across repos (a product "how does
this tool work" guide, statusline/config setup) stay as host built-ins — don't
re-author them here.

## Delegation policy

- **Delegate to keep the conclusion, not the context.** If answering means
  reading across many files, hand it to **explorer** and keep its summary —
  don't pull all that into the main context.
- **Plan before large or ambiguous work.** Non-trivial implementation gets a
  **planner** pass (or the host's plan mode) to reach alignment *before* code.
  Update the plan when the approach changes — don't bury the change in memory.
- **Fan out independent work in parallel.** Launch independent agents in a
  single batch so they run concurrently; don't serialize work that has no
  dependency between the pieces.
- **Don't double-run.** Once you've delegated a search/task, wait for the
  result — don't also do it yourself.
- **Continue, don't restart.** If a host lets you resume a prior agent with its
  context intact, prefer that over spawning a fresh one for a follow-up.
- **Teach vs. do.** Route to **coding-teacher** only when the user wants
  understanding. When they want the change made, make it — don't lecture.
- **Verify findings adversarially.** For review/audit, prefer a **reviewer**
  that tries to *refute* each finding (and, for thorough passes, multiple
  independent verifiers) over a single confident assertion.
- **Send agent-authored work to a different context than wrote it.** The
  **adversary** only works if it did not read the implementer's plan — an
  adversary that inherits the author's context inherits the author's misreading
  and can only confirm. Spawn it fresh; do not continue the implementing agent.
  See `ai/08-adversarial-review.md`.
- **Allocate before you fan out.** Parallel streams on one codebase need a
  written frozen/owned/shared allocation agreed *before* any of them starts, or
  they collide on the few files every feature must touch. See
  `ai/09-parallel-streams.md`.

## Multi-agent orchestration (when a repo opts in)

For comprehensive work — broad audits, migrations, "be exhaustive" reviews —
structure it as fan-out → verify → synthesize: parallel finders, each finding
adversarially verified by independent skeptics, then a synthesis pass, looping
until rounds come up dry. This is a deliberate, heavier mode; use it when the
user asks for thoroughness or opts into orchestration, not for quick checks.
