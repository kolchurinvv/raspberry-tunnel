# Quality ratchets

Execution-policy rule 4 says quality gates move one way: up. This module is the
machinery that makes that hold when most of the code is written by agents. Each
agent can loosen a gate one plausible-sounding reason at a time, and every one
of those reasons looks fine in its own diff.

> Placeholders in `<angle-brackets>` are bound in the repository's Bindings.

## 1. Floors per area, set just under reality

- **A global floor, plus one for every area with real logic.** That means
  domain modules, data access, and the routes or screens that carry business
  rules. An aggregate floor alone lets a weak area hide behind strong ones.
- **A new floor goes just below the number that landed,** never at a round
  number far below it. A floor far under reality protects nothing.
- **Raise the floor when the measured number rises well above it,** in the
  same change or the next one. Next to the floor, record when it was raised and
  what was measured. That makes the next raise obvious, and any lowering
  visibly a step back.
- **Floors are not only line coverage.** Floor every counted measure the
  repository has: branch and function coverage, documentation coverage of
  public interfaces, the share of UI text routed through translation, and so on
  (`<floors>`).

## 2. The floor gate is the local gate

- **One command, locally and in CI.** Floors are enforced by the same command
  in both places, and that command is part of `<gate-command>`. A plain test
  run that never computes coverage isn't the gate. When "green locally" doesn't
  mean "green in CI", agents learn to push and see.
- **Check where each floor is enforced** when you add it. A floor that is
  configured but never enforced is decoration.

## 3. Lowering is a decision, never a fix

- **A red floor is fixed by tests or code.** Never by editing the number.
- **Lowering needs recorded approval.** Lowering a floor, disabling a check, or
  excluding files from measurement needs the user's approval recorded in the
  commit message (`ai/00` rule 4).
- **Shrinking what is measured is lowering.** Excluding a path from coverage,
  or widening an ignore pattern, counts.

## 4. One exemption registry per family of gates, nothing inline

- **One file holds every exemption** from a given family of gates
  (`<exemptions>`). Each entry says exactly what it exempts (the file and the
  specific occurrence) and why.
- **No inline escape hatches where a registry exists.** That means annotations
  at the violation site, the "allow" or "ignore this line" kind. The gate fails
  when it finds one. Any author can add such an annotation in passing; a
  registry turns every exemption into a diff on one file that review can't
  miss.
- **Adding or changing an entry needs the user's approval** recorded in the
  commit message. Agents never grant themselves an exemption.
- **An entry that matches nothing is stale, and fails the gate.** Remove an
  entry in the same change that removes what it exempted.

## 5. Grandfather lists only shrink

When a new invariant arrives over code that already breaks it, don't wait for a
clean slate, and don't exempt by pattern:

- **Freeze today's violations.** List them by name, freeze the list, and fail
  on any new violation.
- **Fail on stale entries too,** fixed or deleted, so the list can only get
  shorter.
- **Keep the gate when the list empties.** Drop only the list.

## 6. Put exemptions out of reach of automated authors

A rule that relies on an automated author to honor it ("only with approval
recorded in the commit message") is a sentence a bot can simply write. Where a
path-based guard controls what automation may change, give exemption registries
files of their own. The guard can then forbid them while still allowing the
files automation legitimately has to touch, such as the list of covered
behaviors. That turns a promise into a mechanism.

## 7. New gates warn first, with a date to fail

A gate introduced over an existing backlog may start as a warning, but only
with a recorded plan to switch it to failing once the backlog is gone. A gate
that only warns blocks nothing (`ai/08` rule 2). Don't count it as a gate until
it fails.

## 8. Gates fail loud

A gate that passes without checking anything is worse than no gate: it reports
safety that isn't there. Every gate fails, with a message saying why, when:

- **It cannot run.** A missing tool, a missing config file, or a dependency that
  won't start is red, never a skip.
- **It saw nothing.** It scanned zero files, collected zero tests, or found no
  baseline to compare against. Assert a minimum, such as at least one test file,
  or the number of modules you expect, so that a moved directory or a broken
  glob fails instead of passing vacuously.
- **It meets a case it doesn't know.** A new file type, an unexpected format or
  an unrecognized option is an error, not something to ignore.
- **It checks by enumeration.** A gate that checks only the paths it lists
  silently skips everything added later. Check everything by default, and
  exempt by entry in the registry (rule 4).

Confirm that a new gate fails by breaking the thing it guards once before you
trust its green (`ai/10` rule 2 applies to gates too).

## 9. Guard the guards

The files that define the gates are the cheapest way to make a change go
green without being right, so they get protection of their own:

- **Tests pin the gate wiring.** The CI umbrella's name and dependencies, the
  absence of `continue-on-error` or `|| true` on gate steps, the members of
  the local aggregate, and each tool-call guard's behavior all have tests. A
  refactor that quietly drops a gate fails (`ai/02` § One umbrella check).
- **Gate-defining files have owners:** gate configuration, exemption
  registries, guard scripts, CI workflows, baselines, dependency manifests,
  and the dev environment's definition. Where the platform enforces code
  owners, changes to these need the owner's review. Where it doesn't, the
  adversarial review's erosion sweep is the backstop (`ai/08` rule 3).
- **Automation gets a diff guard with two tiers:**
  - **veto:** an automated change that touches gate configuration, exemption
    registries, guard scripts, CI, baselines or dependency manifests is refused;
  - **flag:** a change to the task runner or test infrastructure is allowed but
    called out for a human.
- **An automated run reports what actually ran:** a manifest of the gates that
  ran, were skipped, or were never reached. Never a bare "passed".
