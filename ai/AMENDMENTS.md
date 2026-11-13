# Amendments

Some kit updates change what a rule asks agents to do. Each of those changes is
recorded here, newest first, in the same commit series that makes it.

An update rewrites `ai/` and the core block in `AGENTS.md`. It can't reach text
the repository wrote itself: a Bindings row, a review skill or command, a copied
agent definition, a contributing guide, a pull request template. Nor can it reach
an agent that is mid-session and still holds the old text. Each entry's
**Check outside `ai/`** names where the old rule may have been restated.

So an update isn't finished until those are reconciled. The repository's
Bindings carry an `Amendments reconciled` row (`Through A<number>`), and
`ai/sync.sh --check` lists every entry newer than that number and exits 3,
the same hold as an unfilled Bindings row, until the row is raised. A first
install sets the row to the newest entry, since there is nothing older to
reconcile. The steps are in `INSTALL.md` § Update.

A repository that wants to keep the old behavior says so in its Bindings or a
decision record, which win over the kit's defaults (core § Sources of truth),
and raises the row.

**Numbers.** The check finds entries by their `## A<number> ` heading and shows
a repository only the entries numbered above its row. An entry numbered below
one that has already shipped is therefore never shown to anyone who reconciled
past it. So an entry takes the next free number at the moment it lands. If
another entry landed while yours waited for review, renumber yours (and the rows
that name it) before it lands. Once an entry has shipped, never renumber or
delete it. `ai/sync.sh --check` fails on a repeated number or one out of order.

An entry looks like this:

```markdown
## A<n> (<YYYY-MM-DD>): <the rule as it now stands>

- **Changed:** the modules and rules whose text changed.
- **Was:** the behavior it supersedes.
- **Now:** what agents do instead.
- **Why:** the failure that made the old rule wrong.
- **Check outside `ai/`:** where an adopting repository may have restated the old rule.
```

## A3 (2026-11-06): Agents propose rules; they don't declare them

- **Changed:** core § Sources of truth.
- **Was:** nothing said who makes working rules. An agent that found a fault in
  its own work could announce a rule for itself ("from now on, …") and go on as
  if that settled it.
- **Now:** the agent proposes the rule to the user and says where it would live
  (the Bindings, a decision record, memory, or a lesson for the kit). It applies
  once the user rules on it and it is recorded there.
- **Why:** a rule announced in a reply lives only in that session. The next
  session never sees it, and the user never decided it. It also tends to be
  fitted to the one failure that prompted it.
- **Check outside `ai/`:** memory entries, docs or Bindings rows where an agent
  recorded a rule of its own without the user's ruling; prompts or skills that
  tell an agent to "add a rule" after a failure.

## A2 (2026-11-06): `ai/` is the kit's; project docs go where `<project-docs>` says

- **Changed:** core § `ai/` is the kit's (new); `ai/06` § Where each kind of
  knowledge lives; the Bindings skeleton's new `<project-docs>` row; `ai/README.md`
  § Adopt; the adoption prompt in `INSTALL.md`; the message `ai/sync.sh --check`
  prints for files the kit doesn't ship.
- **Was:** the kit said not to edit its files, and `ai/06` put runbooks under
  `docs/runbooks/`. But nothing agents read every session said that `ai/` takes no
  new files or where a project's own docs go, and the check that rejected a project
  file didn't say where to move it. A project file could end up under `ai/`, named
  like a kit module, and listed in `ai/.kit-manifest` by hand to quiet the check,
  and the next update then deleted it as no longer shipped.
- **Now:** nothing project-owned lives under `ai/`, and nobody edits the manifest.
  The repository's own docs go where its `<project-docs>` Bindings row says.
- **Why:** every update replaces `ai/`. Anything else there is overwritten,
  deleted, or collides with a kit module that has the same number.
- **Check outside `ai/`:** add the `<project-docs>` row (`--check` asks for it).
  Then look for files under `ai/` that the manifest doesn't list, or that you
  listed by hand (`git log -p -- ai/.kit-manifest` shows lines added outside an
  update); a file a past update deleted that way is still in git history. Move
  them to `<project-docs>` and fix links to their old paths. Also look for
  Bindings rows or docs that tell agents to restore files under `ai/` after an
  update.

## A1 (2026-11-06): Adversarial review is one pass

- **Changed:** core rule 10; `ai/08` § The review-fix loop (steps 1, 5 and 6,
  § Why one pass, § Automated loops); `ai/00` § Definition of done, item 5;
  `agents/adversary.md` § Handoff protocol; `ai/07` § Multi-agent
  orchestration; the Bindings skeleton's `Adversarial review (core 10)` row.
- **Was:** "every finding gets an answer", with nothing in the core about when to
  stop, read as review, fix and review again until the reviewer finds nothing.
- **Now:** before the review, the author puts its own tests through every
  `ai/08` Rule 5 check and keeps them on real dependencies (`ai/10`), so the
  review isn't spent finding what the author could have run. Then one review.
  Every finding is fixed, rebutted with evidence, or held for
  the user's ruling, and then the loop stops. A further round happens only when
  the user asks, and covers the delta since the last review. A defect class that
  keeps coming back in new places goes to the user as a design question, not as
  another round of patches.
- **Why:** repeated rounds don't converge. The real defects surface in the first
  round or two. Later rounds restate closed findings, critique the previous
  round's fixes, or reach into code nobody touched, and each one costs a full
  review and a fix pass.
- **Check outside `ai/`:** the `Adversarial review (core 10)` Bindings row; any
  review skill, command or agent copy that runs the loop (`providers.md`
  § Loading the kit's agents); contributing or pull request template text that
  asks for a clean re-review; agents mid-session, which keep the old rule until
  restarted or told.
