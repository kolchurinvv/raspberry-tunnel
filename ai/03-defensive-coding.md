# Defensive coding

**The application must never crash from an upstream, external, or system
failure.** Networks time out, peers return malformed payloads, local storage
isn't writable (no permission, disk full, missing entitlement), native/FFI
modules throw. The correct response is always graceful degradation: safe
default, no-op, fall back, log and continue. Never a crash, never an unhandled
rejection.

This is language-agnostic; the canonical patterns differ but the boundaries are
the same.

## The six boundaries

1. **Every call into native / FFI / external code has a failure boundary.**
   Synchronous calls into native code are especially dangerous — an exception
   can propagate across the language bridge and abort the process. Wrap them;
   log a warning; return a safe fallback (`null`/`None` for reads, no-op for
   writes). Factor the wrapper once (a `safeStorage`-style helper) rather than
   scattering try/catch.

2. **Every parsed external payload is schema-validated before use.** Anything
   crossing a trust boundary — HTTP response, config file, deserialized
   storage, IPC/queue message — is validated against a schema before you touch
   its fields. Trusting `JSON.parse(...)` / a backend response to be the right
   shape is a defect waiting to happen. Reuse the project's validation library.

3. **Every storage read assumes the value might be missing OR malformed.**
   Coerce and validate after reading. Old/legacy shapes must degrade
   gracefully, not throw.

4. **Every async path has a failure branch.** A promise/future without a
   rejection handler is a bug. An `await` without a surrounding guard is a bug.
   Background tasks and effect callbacks that can reject need their own internal
   handling — an unhandled rejection there can still take down the process.

5. **Failure logs are debuggable.** Include the operation, sanitized inputs (no
   tokens, no passwords, no PII), and the error. Use `warn` for "recovered, but
   worth knowing"; `error` for "recovered to a degraded state that matters."

6. **Crash deliberately only for invariant violations.** Programmer-error
   assertions — unreachable states, exhaustive type-narrowing defaults,
   "this can't happen" — are *fine* to throw; they catch bugs early. **External
   failure is never an invariant violation** and must never be modeled as one.

The distinction in rule 6 is the whole point: fail loud on *your* bugs, fail
soft on *the world's* failures.

## Failing loud

The soft half above keeps the app alive. The loud half keeps a change from
quietly breaking what already worked, and it matters most when agents write
most of the code. An agent's mistake that throws is found in the next test run;
one that falls back silently is found by a user, weeks later.

- **No catch-and-continue around your own code.** Catch where you can do
  something meaningful (an external boundary, rule 1), and let everything else
  propagate. A catch that logs and carries on inside your own logic hides the
  bug at the point it happened.
- **No defaults for values that cannot be missing.** If the type, the schema
  or an earlier validation guarantees a value, don't paper over its absence
  with a fallback (an empty list, a zero, an optional chain). Validate once at
  the boundary (rule 2), then trust it; an absence past that point is a bug, so
  throw.
- **Exhaustive handling.** Every branch on a closed set (an enum, a tagged
  union, a status) handles every case, and its default throws. A default that
  quietly does nothing turns the next added case into a silent no-op.
- **Unknown input to your own functions is an error.** An unrecognized option,
  mode or key raises, rather than being ignored.
- **Tests pin the failure too.** For each invariant, a test asserts that
  breaking it raises (`ai/10`).
- **The same goes for tooling.** A script or check that cannot do its job (a
  missing tool, no input, nothing matched) exits non-zero and says why. It never
  reports success (`ai/11` rule 8).
