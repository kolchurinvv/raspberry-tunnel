# D5: A status request may cost up to 250 ms of CPU on a Pi 3 B+

- **Date:** 2026-11-13
- **Status:** accepted; amends D2 (the status endpoint's CPU budget per request)
- **Decided by:** the user, on 2026-11-13, after the first measurements on the
  real Pis: "200 give or take irl is fine", and then 250 ms as the budget. They
  took neither of the two other options below. How the cost is measured on a Pi
  is the agent's, and is how the VM test already measures it.

## Context

D2's contract for the Pi status endpoint set a budget of under 150 ms of CPU per
request on a Pi 3 B+. That number came with the contract, before the endpoint
existed.

The VM test measures a request's CPU time as the change in `usage_usec` in the
`cpu.stat` of the endpoint's slice, `system-pi*status.slice`, across 10
requests. Its runs logged 16.0 ms per request on Pi A and 18.5 to 18.7 ms on
Pi B. Those are x86 VMs, so they say little about a Pi.

Both Pis were updated on 2026-11-13 and measured the same way, over 10
requests: 170 ms per request on Pi A and 206 ms on Pi B. Both are over 150 ms.
Pi B costs more because a gateway also sends one disco ping to its exit node and
runs `ip` once, to read rule 5260.

The dashboard asks each Pi every 30 seconds (D2), so 206 ms per request is
about 0.7% of one core, or 0.17% of the Pi's four.

The options:

- **Trim the script.** jq is likely its biggest single cost. The script has
  already been trimmed once (`modules/status.nix`): bash builds most of the
  reply, because a jq program for the whole reply took about twice the CPU, and
  nc stands in for curl.
- **A compiled endpoint** in place of the script. It would cost less CPU, but it
  needs an ARM build of its own and more code to maintain.
- **Raise the budget** to fit what the Pis spend.

## Decision

- **The budget is under 250 ms of CPU per request on a Pi 3 B+.** So D2's
  Load bullet reads "under 250 ms" where it says "under 150 ms". The rest of
  D2's contract is unchanged.
- **It's measured as the VM test measures it:** the change in `usage_usec` in
  `/sys/fs/cgroup/system.slice/system-pi*status.slice/cpu.stat` across a run of
  requests, divided by their number.
- **The endpoint stays the script it is.** It isn't trimmed further for this,
  and isn't replaced by a compiled program.

## Consequences

- Both Pis are within the budget: Pi A at 170 ms, Pi B at 206 ms. Pi B has
  44 ms of room, little more than the 36 ms a gateway's extra work, with its
  disco ping and `ip`, adds over Pi A. So a change that adds a process to every
  request can use up most of it. Measure on a Pi after such a change.
- The budget is per request, so the load grows with how often the Pis are asked.
  The figures above are for D2's 30-second interval.
- A Pi measured over 250 ms per request is over budget. The two options not
  taken are the first places to look, and a higher budget would take a record
  of its own.

## Enforcement

No gate measures CPU on a real Pi.

- The VM test (`checks.x86_64-linux.tunnel`) logs each Pi's CPU time per
  request, but on x86 VMs, where a request costs about a tenth of what it does on
  a Pi 3 B+. It fails on a reply slower than 2 seconds, never on CPU time. So
  its log shows a change in cost, not whether a Pi stays under 250 ms.
- On the Pis, the cost is measured by hand after a deploy. On the Pi, read the
  slice's counter, in microseconds:

  ```sh
  awk '/^usage_usec/ {print $2}' /sys/fs/cgroup/system.slice/system-pi*status.slice/cpu.stat
  ```

  Then send 10 requests from one of your own devices on the tailnet, and read it
  again:

  ```sh
  for i in $(seq 10); do curl -fsS -o /dev/null http://<pi-tailnet-address>:9190/status; done
  ```

  The difference divided by 10,000 is the CPU time per request in milliseconds.
  A dashboard request that lands between the two reads counts too.
