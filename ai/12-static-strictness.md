# Static strictness and invariant guards

The cheapest bug is the one the machine refuses to accept. This module covers
the checks that run without executing the code: the type checker, the linter,
the formatter, and guard scripts that hold the project's own invariants. With
agents writing most of the code, they are the first reviewer every change
meets, and the only one that never gets tired.

> Placeholders in `<angle-brackets>` are bound in the repository's Bindings.

## 1. Every check is a hard gate

- **Warnings are errors.** Configure each tool so that anything it reports
  fails the gate: a zero-warnings flag, or every rule set to error. A rule at
  warning level is decoration that agents learn to scroll past. If a rule isn't
  worth failing on, turn it off on purpose, through the exemption registry
  (`ai/11` rule 4).
- **Formatting is checked, not suggested.** A formatting check runs in the
  fast tier, and formatting is never a review comment.
- **Every check is in the aggregate** (`<gate-command>`), with the same
  configuration locally and in CI.

## 2. The strictest settings the stack offers

- **The type checker in its strictest mode,** plus the extra checks that catch
  what agents get wrong most: unchecked access to indexed collections, implicit
  fall-through in branches, implicit overrides, implicit "any".
- **Lint with type information where the stack supports it.** The rules that
  catch dropped async results, promises passed where a value is expected, and
  unhandled failures need types to work. A syntax-only lint misses exactly the
  bugs `ai/03` is about.
- **The escape hatches are banned or registered,** never sprinkled: type
  suppressions, non-null assertions, casts to "any", lint-disable comments
  (`ai/11` rule 4).
- **Tool versions are pinned and match CI.** A gate that passes locally on a
  different version proves nothing about CI.

## 3. Architecture rules are lint rules

When a rule about the code's structure matters, the linter enforces it, with a
message that says why:

- *this module is the only one that writes to that store;*
- *navigation state goes through this helper, never through route parameters;*
- *the database client is imported only by the data layer.*

Restricted imports and restricted syntax selectors cover most of these. The
message matters: it is what the next agent reads at the moment it is about to
break the rule. A rule that lives only in prose is one that gets broken the
first time nobody re-reads the prose.

## 4. Invariant guards for what the linter can't express

Some invariants span files, or are about what's present rather than what's
written:

- every write to a store goes through one module;
- every place that needs the current time goes through one clock;
- every identifier a test uses exists in the app;
- no secret is ever committed;
- events come from a closed vocabulary.

Write a small guard script for each, and run it in the fast tier. Every guard:

- **matches on code, not comments.** Strip comments with the language's own
  parser, so a commented-out example neither triggers it nor hides from it.
- **matches what a violation must do,** not what it is called. Renaming a
  helper must not slip past.
- **fails loud** (`ai/11` rule 8). Give it a minimum number of files it must
  scan, and have it fail if it can't parse a file it was meant to check.
- **has its own tests:** a fixture that violates it, and one that doesn't. A
  guard nobody tested is a guard nobody knows works.
- **takes exemptions only through the registry** (`ai/11` rule 4), or through a
  grandfather list that only shrinks (`ai/11` rule 5).

## 5. Generated code is checked for drift

When code is generated from a source (schemas, protocol definitions, design
tokens), the gate regenerates it and fails if the result differs from what's
committed. Hand edits to generated files, and sources changed without
regenerating, both turn red.
