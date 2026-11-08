# Test discipline

What tests must prove. A test that passes without proving the behavior it names
is worse than no test: it turns a gap into a green check. Reviewing tests
someone else wrote is `ai/08-adversarial-review.md` rule 5; this module is the
same standard from the side of the one writing them.

> Placeholders in `<angle-brackets>` are bound in the repository's Bindings.

## 1. Verify persistence by reading it back

For any interaction that should persist something (a form submit, an API write,
a background job's output, a message consumed and stored), integration and
end-to-end tests **read the record back from the real store** and assert on
what was stored. Asserting on the response, the UI's confirmation, a log line,
or a mock is not verification.

**Why:** a response or a success message proves the request was handled, not
that the write happened. Writes get lost, stored with the wrong shape, attached
to the wrong record, or silently dropped by a mocked or misconfigured store.
Each of those passes a test that only checks what the caller saw.

**How:**

- Read back through the store's own access path: the repository's data layer,
  or a test handle on the real database (`<read-back>`). Never read it back
  through a cache that the code under test also uses.
- Assert on the stored fields that matter, including those the caller never
  sees: owner, status, timestamps, foreign keys.
- Updates and deletes get a read-back too. The new value, or the absence of the
  record, is the stored state to assert on.
- Unit tests with a fake store don't satisfy this rule. They show the code
  called the store, not that the store kept anything.
- A test of a persisting interaction with no read-back is unfinished. In review,
  it's a finding (`ai/08` rule 5).

## 2. A test must be able to fail

- **Check it fails without the change.** Reason it through, or run it against
  a revert when that is cheap. A test that passes either way pins nothing.
- **Derive expected values from their source.** Never paste in what the code
  happens to output today.
- **Don't weaken a test to make it pass.** Deleting an assertion, loosening it,
  or skipping the test lowers a gate (`ai/00` rule 4).

## 3. Test against the real thing; quarantine fakes

- **Data-shaped tests run against a real instance of the store,** such as an
  in-memory or disposable real server. Not against a hand-written fake: a fake
  agrees with whatever the code under test assumes, so it passes exactly the
  mistakes you most need to catch.
- **Fakes exist only for misbehavior** a real dependency can't be ordered to
  produce: refusing a connection, timing out, losing a race, returning a
  malformed value.
- **Keep the fakes in one place that exports nothing,** so they can't be
  imported around the rule.
- **Guard the quarantine statically.** Match what any fake must do, such as
  building a stand-in for the client's type or mocking the client's module,
  not the names of helpers. Names change; the cast doesn't.

## 4. Cover behaviors, not only lines

Line coverage shows that code ran, not that each behavior a user or caller
relies on is exercised end to end.

- **Keep a manifest of behaviors:** routes, screens, jobs, commands, each with
  the end-to-end test that covers it.
- **Check the manifest against the code,** so a new behavior can't arrive
  without an entry.
- **Fail on a behavior that has neither a test nor an exemption** in the
  registry (`ai/11` rules 4 and 8).

## 5. Test first, by layer

How strictly the test comes first depends on what is changing:

- **Pure logic and validation** (code without I/O: parsers, validators, domain
  rules): strictly test first. Write the test, run it, and watch it fail for
  the reason you expect. Only then write the code. A test you never saw fail
  may be testing nothing.
- **Interfaces between parts** (HTTP handlers, message consumers, public module
  APIs): the contract test comes first, input in and response plus stored state
  out (rule 1). Then the implementation.
- **UI, end-to-end flows and native integrations:** tests may follow the code,
  but in the same change, never "later".
- **Every bug fix starts with a failing test** that reproduces it, at the lowest
  layer that can show it. The fix makes it pass, and the test stays.

Whatever the layer, a change isn't done until its tests are in the same commit
series.
