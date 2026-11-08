# Install, update or remove the kit

Step by step, for a repository that already has code, history and other people
working in it. The installer creates the documents and leaves git alone: no branch
and no commit. How the change lands is up to the repository's maintainers. Why it
is shaped this way: `README.md` § Adopt it in a repository.

**What the installer touches:** it adds `ai/` (including `ai/.kit-manifest`) and
writes the core rules into `AGENTS.md`, creating the file if it's missing.
Nothing else, and it leaves git alone. The rules sit between two marker lines. If
the file has neither, the installer adds them before the first heading after the
title, or at the end; move them if the rules belong elsewhere, as long as they
stay above the Bindings, and later runs keep them there. If the file has no
Bindings section, it adds one after the rules. It refuses to overwrite an `ai/`
directory it didn't create, or one with uncommitted edits, and to create
`AGENTS.md` anywhere but the root of a git repository.

**What it ships:** `ai/` only. The kit repository's `lessons/` directory is an
inbox of candidate rules, and nothing in it reaches your repository. A lesson
arrives only after it is promoted into an `ai/` module, with your next update.

**You need:** `bash`, `curl`, `tar`, `git`. The kit repository is public, so no
GitHub account or token.

To hand the whole adoption to an agent instead, use the prompt in § Adopting
with an agent.

## Install

### 1. Decide how the change lands

The installer works on whatever is checked out. Where the repository takes
changes through a branch and review, start the branch first, named the way the
repository names branches (a ticket key, a type prefix); `add-agent-contract`
below is a placeholder.

**A normal clone:**

```sh
cd path/to/repo
git fetch origin
git switch -c add-agent-contract origin/main
```

**A bare repository with one worktree folder per branch** (a `.bare/` directory
next to folders like `main/`):

```sh
cd path/to/repo-root                # the folder that holds .bare/
git -C .bare fetch origin
git -C .bare worktree add --no-track -b add-agent-contract ../add-agent-contract origin/main
cd add-agent-contract
```

`--no-track` keeps the new branch from pushing to `main` by accident.

If git says `invalid reference: origin/main`, the bare repository was cloned
without a fetch refspec (a plain `git clone --bare` doesn't set one). Set it
once, then fetch and run the `worktree add` line again:

```sh
git -C .bare config remote.origin.fetch '+refs/heads/*:refs/remotes/origin/*'
git -C .bare fetch origin
```

### 2. Run the installer

From the repository's root:

```sh
curl -fsSL https://raw.githubusercontent.com/kolchurinvv/ordain/main/install.sh | bash
```

It downloads the kit at one exact commit, copies `ai/`, records that commit and
a hash of every file in `ai/.kit-manifest`, and writes the core rules and a
Bindings table into `AGENTS.md`. Then it runs `ai/sync.sh --check .` on the
result. Bindings rows still to fill in are expected at this point, so it names
them and carries on; any other failure stops the install.

To read the script before running it, download it first:
`curl -fsSL … -o /tmp/install.sh`, read it, then `bash /tmp/install.sh`.

Options go after `bash -s --`:

| Option | Does |
|---|---|
| `--ref <tag-or-sha>` | Install a specific kit version instead of `main`. |
| `--force` | Overwrite an existing `ai/` the installer didn't create, or create `AGENTS.md` outside a repository's root. Only when you mean to. |
| `<path>` | Install into that repository instead of the current directory. |

### 3. Fill in the Bindings

Directly after the rules, the installer added the `## Bindings` table below,
unless the file already had one. It maps the kit's generic rules onto this
repository, so this is the step that takes someone who knows the repository.
Replace every `TODO`; `none: <why>` is a valid value, a blank is not.

```markdown
## Bindings

| Binding | Here |
|---|---|
| `<gate-command>` | TODO: The command that runs the checks before a commit, and what to do where it can't run. |
| `<base>` branch | TODO: The branch changes merge into. |
| Branches and commits | TODO: The repository's naming and message format. |
| Decision records | TODO: Where architecture and stack decisions are written down. |
| Status docs (rule 5) | TODO: The README or roadmap that must stay current. |
| Generated baselines (rule 3) | TODO: Snapshots, fixtures, migrations, lockfiles: files agents must never rewrite unasked. |
| `<read-back>` (`ai/10`) | TODO: How tests read a stored record back from the real store: the database or store, the helper or test handle, how test data is isolated. `none: <why>` if nothing persists. |
| `<floors>` (`ai/11`) | TODO: Where the floors are configured, the command that enforces them (part of the gate), and the current global numbers. `none: <why>` if nothing is measured yet. |
| `<exemptions>` (`ai/11`) | TODO: Where each family of gates keeps its exemption registry. `none: <why>` if no gate takes exemptions. |
| `<static-checks>` (`ai/12`) | TODO: The type-check, lint, format and guard commands, whether warnings fail, and the invariants the guards hold. State gaps (`none: no lint gate yet`). |
| Quality gates (rule 4) | TODO: The checks that may only get stricter. |
| CI (rule 6) | TODO: What runs on which event, and how to watch it. |
| Adversarial review (core 10) | TODO: How independent review is run here. |
| Agent definitions | TODO: Where agent and skill files live. |
| Memory root | TODO: Where shared agent memory lives, or none. |
| Autonomy | TODO: What agents may do without asking. |
| `Feedback (star-pupil)` | off |
| The kit itself | Vendored from ord-ai-n (version in `ai/.kit-manifest`). Update by re-running the installer; never edit `ai/` or the block above. |
```

**Reconcile, don't stack.** Where the repository already has rules (a
constitution, a CONTRIBUTING file, a commit convention, its own review skill),
point at them in Bindings and say which document wins. Don't copy them in.

**Describe what is, gaps included.** Find each value in the repository itself
(its scripts, CI, tests, docs) rather than guessing. Where the repository
doesn't meet a rule yet, the row says so: for example, "no test reads a stored
record back yet; start from <the test database helper>". Fixing the gap is
separate work, not part of adopting.

### 4. Wire the tools that don't read `AGENTS.md`

Most coding agents read `AGENTS.md` by themselves. Some read their own file
instead, or stop reading `AGENTS.md` once their own file exists. For each tool
the repository's contributors actually use, make its entrypoint import
`AGENTS.md` rather than copy it. `providers.md` says how, per tool.

### 5. Check, commit, open the PR

The installer ran the check when it finished. Check again now that the Bindings
are filled in. Re-running the install line does that too, and works before you
commit:

```sh
ai/sync.sh --check .
git add ai AGENTS.md   # plus any entrypoint files from step 4
git commit        # the repository's message format; name the source line from ai/.kit-manifest
```

`--check` also fails while a Bindings row is blank or still says `TODO`, or while
a placeholder from the skeleton in step 3 has no row, and it names them. After an
update that adds a placeholder, that tells you which row to write.

Before opening the pull request:

- **Run the repository's gate** (its `<gate-command>` binding), the way the
  repository requires before a PR. Check that shared resources such as a test
  database are free first. Never copy another checkout's secrets file to make
  the gate run (`ai/01` § The gate). If you can't run it, say so in the PR
  instead of implying it passed.
- **Check overlap with other open PRs** that touch `AGENTS.md` by merging your
  branch with theirs, not the base with theirs (`ai/01` § Pull requests).
- **One concern per PR.** Adopting means adding `ai/`, the block, the Bindings
  and the entrypoint wiring. Pointing one of the repository's existing tools at
  the kit is a change to something its maintainers already run, such as wiring
  a review skill to `ai/08`. That goes in its own PR, which says what the tool
  will start doing or flagging. If that PR is stacked on the adoption PR, see
  `ai/01` § Stacked pull requests: it may get no CI until it is retargeted.

Open the pull request the way the repository usually does. Its body says:

- what the contract is;
- what the Bindings map, and which of the repository's own documents win;
- how the gate went;
- that your files merge cleanly with the other open PRs;
- how to update the kit (§ Update).

Then, as a separate change, make CI fail when someone edits the kit in place.
The kit repository is public, so whoever maintains the repository can see where
a fix belongs. GitHub Actions:

```yaml
  agent-contract:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: ai/sync.sh --check .
```

### 6. Turn every gap into an issue

A Bindings row that says `none`, or states a gap, is work the repository
doesn't have yet. Open one issue for each, in the repository's own terms,
so the gaps get closed rather than just recorded. Typical ones:

- no single gate command, or none run before commits (`templates/gate.just`,
  `templates/pre-commit`);
- lint warnings that don't fail, a type checker below its strictest mode, no
  formatting check (`ai/12`);
- no coverage floors, or one global floor and nothing per area (`ai/11`);
- inline escape hatches instead of an exemption registry (`ai/11`);
- tests that don't read persisted data back from the real store (`ai/10`);
- no umbrella check, or merges that don't require a green head (`ai/02`).

Each issue goes through the repository's normal process, like any other change.
Don't fold them into the adoption PR.

## Adopting with an agent

To have an agent do the adoption, paste this into whichever agent works in the
repository. It names no tool and needs no filling in. It carries steps 1–2
itself, because the kit isn't in the repository yet, and hands over to this file
from step 3 on.

```text
Adopt the ord-ai-n agent kit in this repository. The kit is a contract for how AI agents work here, whatever the tool or model. The result is a change for the maintainers to review, landed the way this repository lands changes. Read this whole message before doing anything.

1. Land this the way this repository lands changes; if its documents don't say, ask me. Where that means a branch, name it the way this repository names branches. If the repository keeps one worktree per branch, add a worktree instead of switching branches in an existing one.

2. Run the installer from the repository root:
       curl -fsSL https://raw.githubusercontent.com/kolchurinvv/ordain/main/install.sh | bash
   It writes AGENTS.md itself, including a Bindings table whose rows say TODO.

3. From here on, follow ai/INSTALL.md, which is now in the repository, from Install step 3 to the end of step 5. It is the procedure; this message only adds emphasis:
   - Bindings: find every value in the repository itself. State gaps instead of fixing them. Where the repository has its own governing documents (a constitution, CONTRIBUTING, decision records), they win over the kit. Say so in the Bindings, and follow them for branch names, commit messages, and what must pass before a pull request.
   - Never edit ai/ or the generated block by hand; only the installer writes them.
   - Before the pull request, run the repository's gate the way it requires. Don't copy anyone's secrets to make it run, and don't take a shared resource (a test database, a container) that another checkout is using. If you can't run it, say so.
   - One concern per pull request. Changes to tools the maintainers already run go in a separate pull request.
   - After the pull request is open, list every gap the Bindings state and offer to open one issue per gap (ai/INSTALL.md step 6). Open them only when I agree.

4. Commit as you go, one concern per commit, in the repository's format. Show me the commits, and any branch and pull request text. Push and open a pull request only when I say so. Then watch its CI to a green conclusion on the pushed head commit, job by job, and report.

Ask me about anything this repository's own documents don't settle: a ticket key, which coding tools contributors use, what may run locally.
```

## Update

1. Decide how the change lands, as in Install step 1.
2. Re-run the install line from step 2.
3. Commit the result on its own. Its diff is exactly what changed in the kit;
   the source line in `ai/.kit-manifest` shows which version you moved to.

If the installer refuses because `ai/` has uncommitted edits, someone edited
the kit in place. Move what they meant into Bindings, drop the edit
(`git checkout -- ai`), and run it again.

## Remove

```sh
git rm -r ai
```

Then delete the marked block and the Bindings section from `AGENTS.md`, the
imports you added to tool entrypoints in step 4, and the CI job if you added
one. Commit.

## Sending lessons back (optional)

The kit ships an agent, the star-pupil (`agents/star-pupil.md`), that turns what
agents learned in this repository into lessons for the kit. It is off until the
`Feedback (star-pupil)` row in Bindings says `on` and names what it may read.

To run it in any tool, ask the agent to act as the agent defined in
`ai/agents/star-pupil.md`. Tools that support sub-agents can also load it as
one (`providers.md`).

1. **It shows its disclosure first.** Before reading anything, it prints a
   table of what it will read, never read, write, send, and strip out, and
   waits for your go-ahead.
2. **Lessons stay local.** It writes them to a local outbox, and nothing leaves
   the machine at this point.
3. **Sending is one pull request.** If you choose to send, it prepares one pull
   request to the kit repository that adds the lesson files under `lessons/`.
   It shows you every file and the PR text, and opens the PR only when you
   approve. The PR is public: anyone can read it. You need a GitHub account to
   fork the kit.
4. **The kit's maintainer reviews the PR.** Merging accepts the lessons as
   candidates. Moving one into the kit's rules is a separate, later commit, and
   the improved rule reaches your repository with your next update.
