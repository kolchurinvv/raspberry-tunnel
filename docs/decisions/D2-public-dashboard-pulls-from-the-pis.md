# D2: A public dashboard shows the tunnel's live health, pulled from the Pis over the tailnet

- **Date:** 2026-11-13
- **Status:** accepted
- **Decided by:** the user.
  - **Public, live data with no sign-in.**
  - **The design** (mockup A), and serving it at the root of the hostname the
    tunnel's Headscale already uses.
  - **Running it on the user's k3s cluster.**
  - **The Pi side with the least performance impact.**
  - **Pulling over the tailnet**, which the user proposed on 2026-11-12. On
    2026-11-13 the user asked whether pushing from the Pis would be lighter, and
    weighed the answer: the load is about the same, and pulling reports failures
    in more detail. They confirmed pulling the same day, accepting the short
    start-up delay of each request.

  The agent proposed the rest, and the user may overrule it:
  - the endpoint's shape and the contract below;
  - the 30-second interval;
  - letting the user's own devices read the endpoint.

  Left to D3:
  - the dashboard's stack, including its storage;
  - where its code lives.

## Context

The user wants to see the tunnel's health at a glance, and to link the page from
their resume and website, so it is public:
- the Pis' temperature, CPU, memory and storage;
- how much traffic the tunnel carries, and whether it is direct or relayed;
- which connection each TV leaves through.

Constraints:

- The Pis are Raspberry Pi 3 B+ boards with 1 GB of memory. Whatever runs on
  them has to stay light.
- Both Pis sit behind NAT, and apartment A also behind carrier-grade NAT, so
  nothing on the internet can reach them. The tailnet is the only way in.
- More apartments may be added later, all leaving through Pi A. Nothing here may
  assume there is only one gateway.
- The page is public. It must not reveal anyone's addresses, and this public
  repository doesn't name the servers or hostnames involved (they are in
  `private/network.md`).

The options for getting the numbers to the dashboard:

- **The Pis push** to a public endpoint. Pi B's own traffic goes through Pi A as
  its exit node, and Tailscale drops it while Pi A can't be reached. So Pi B
  would go silent exactly when the tunnel fails, unless its routing gets an
  exception. Pushing over the tailnet instead would avoid that, but a missing
  push can't tell a dead Pi from a dashboard that can't receive.
- **The dashboard joins the tailnet and pulls.** Traffic between two tailnet
  nodes doesn't go through the exit node, so Pi B stays reachable while Pi A is
  down, and Pi B's routing doesn't change. A failed pull also says how it failed:
  the node is offline, it timed out, or it answered with an error. Chosen.

The options on the Pis:

- **Prometheus `node_exporter`.** Its data is read the same 30-second way, by a
  process that stays in memory, about 15–20 MB on a 1 GB Pi. The tunnel-specific
  numbers would still need a script on a timer.
- **A socket-activated endpoint.** Nothing stays in memory between requests. Each
  request starts a short script that reads what it needs once.

Per read, the work is much the same either way. What decides it is that the
endpoint keeps nothing resident. The user asked for whatever affects the Pis'
performance least, so the endpoint was chosen.

## Decision

1. **Public and live.** Anyone can open the dashboard, and it shows real data as
   it is collected, with no sign-in. The sign-in design (passkey first, then
   email and password) is shelved, not dropped. The user accepts that live
   traffic shows when a TV is in use.
2. **The design is mockup A, the overview.** Its source is in
   `private/dashboard-mockups/`, and it shows the states described here.
   - **The header** has the name "raspberry tunnel", with "raspberry" in berry
     red, and a logo of a raspberry drawn as a mesh of connected nodes. Beside it
     are:
     - a 1 h, 24 h, 7 d or 30 d range switch;
     - a colour scheme switch. It follows the system's light or dark setting
       until the viewer picks one.
     - an icon-only GitHub link to this repository.
   - **A status banner.** Its level is the worst of these conditions:
     - **Critical:** the exit node can't be reached, or any gateway has its TV
       on the apartment's own connection.
     - **Warning:** any tunnel is relayed, or the exit check doesn't match, or
       any Pi shows undervoltage or is at 60 °C or above.
     - **Good** otherwise.

     It also shows how much of the selected range the TVs spent on the tunnel.
   - **A diagram of the traffic paths.** It has one row per gateway apartment
     (TV, then its Pi, then its tunnel), and the tunnels all converge on Pi A,
     then the internet. The Headscale server sits above, marked as carrying no
     traffic.
   - **One card per Pi, all with the same fields.**
     - Liquid-fill circle gauges:
       - tunnel traffic, in a large one;
       - temperature, on a 20–85 °C scale, amber from 60 °C;
       - CPU, memory and SD card, amber from 85%.

       Each value sits inside its circle. It is drawn twice, clipped to the
       liquid and to the empty part, each copy coloured with CSS
       `contrast-color()` and a fixed fallback.
     - Below the gauges: role, uptime, power and last report. Power is fine
       when there is no undervoltage now and none counted since boot.
     - The traffic gauge's full scale is open until the tunnel's real
       throughput has been measured. 50 Mbit/s stands in until then.
   - **A Headscale card**, with:
     - the cluster node hosting Headscale: its CPU and memory;
     - the tunnel's tagged nodes: whether each is online, when it was last seen,
       and when its key expires.
   - **An exit route strip**, with one row per gateway's TV. TV A has no row: it
     sits on apartment A's own router and nothing measures it. Each moment is
     one of four states:
     - **Tunnel:** the gateway is up, with no fallback rule.
     - **Own connection:** the fallback rule is set.
     - **Held:** the tunnel is down but there's no fallback rule yet, or the TV's
       port is down.
     - **No data:** the gateway didn't answer.
   - **Line charts** for tunnel traffic, tunnel latency, temperature (with a
     line at 60 °C, where the Pi lowers its clock) and CPU. Hovering one moves a
     marker across all of them.
   - **Events**, which the dashboard derives from consecutive samples:
     - a path change (direct, relayed or offline);
     - fallback set or cleared;
     - a Pi becoming unreachable or reachable again;
     - a reboot (uptime went down);
     - undervoltage seen.
   - **Checks:**
     - the exit address matches;
     - each TV port is gated;
     - each fallback rule is off;
     - power;
     - no key expires within 30 days.

   Apartments are named after their Pi, and Pi A is the only exit.
3. **The dashboard runs on the user's k3s cluster.**
   - It is public at the root of the hostname the tunnel's Headscale already
     uses (in `private/network.md`). The reverse proxy in front of Headscale
     keeps Headscale's routes, and forwards only the dashboard's own paths into
     the cluster: `/` and one prefix of the dashboard's own. The dashboard never
     uses `/api/`, which belongs to Headscale.
   - It joins the tunnel's tailnet as its own node, tagged `tag:dashboard`.
   - It asks every Pi for its status every 30 seconds and keeps 30 days of
     history.
   - The user deploys it. Agents never touch the cluster.
4. **The Headscale card needs no Headscale credentials.**
   - **Tagged nodes.** Online state, last seen, key expiry and tags come from the
     dashboard's own tailnet node. Headscale sends those for every peer, and the
     policy lets the dashboard see only the Pis.
   - **CPU and memory** of the cluster node hosting Headscale come from the
     cluster's metrics API (`metrics.k8s.io`), through a read-only RBAC rule.
   - **No Headscale API key.** Its keys can't be scoped, so a public service
     must not hold one.
5. **Each Pi serves the read-only endpoint in the contract below.** It is
   socket-activated, so nothing runs between requests, and runs without
   privileges. The firewall opens it on `tailscale0` only.
6. **The Headscale policy lets in only `tag:dashboard` and the user's own
   devices (`tunnel@`),** on that port only.
7. **No addresses on the public page.**
   - The dashboard never publishes an IP address, a WireGuard endpoint, a device
     name or its own tailnet status. It shows only the tagged Pis and the
     Headscale server.
   - The Pis send their public address only for the exit check. The dashboard
     compares the addresses as they arrive, stores and publishes only whether
     they match, and keeps neither address.

## Contract: the Pi status endpoint, schema 1

The Pi stream builds this, and the dashboard stream reads it (`ai/09`). Neither
changes it without the other, and a change goes through this record.

- **Transport.** `GET /status` over HTTP on TCP port 9190, at the Pi's tailnet
  address.
  - Any other path gets a 404.
  - The answer is `application/json`, one object, built fresh for each request.
  - The connection closes after the reply.
- **Timing.**
  - The reply must arrive within 2 seconds, including while the exit node is
    unreachable. Every step that waits has a deadline of its own.
  - Nothing that leaves the Pi happens on the request path, except the one disco
    ping through tailscaled.
  - The service has a runtime limit, and the socket accepts only a few
    connections at once.
- **Load.** No process stays running between requests.
  - Tailscale data comes from tailscaled's local API over its Unix socket, never
    from the `tailscale` CLI.
  - The budget is under 150 ms of CPU per request on a Pi 3 B+.
- **On failure.** If a value can't be read, its field is `null`. It is never
  left out and never replaced by a made-up number. If building the answer fails
  as a whole, the reply is HTTP 500 with `{"schema": 1, "error": "<what failed>"}`.
- **Counters** are cumulative. Any decrease means the counter reset, through a
  reboot, an interface being recreated, or a WireGuard peer being recreated. It
  is never negative traffic.
- **Units.**
  - CPU `stat` is in USER_HZ ticks.
  - `kb` fields are KiB, as `/proc/meminfo` reports them.
  - `_b` fields are bytes.
- **Time.** `time` comes from the Pi's clock, which can be wrong early in a
  boot. It is for information only. The dashboard timestamps every sample with
  its own clock.
- **Compatibility.** New fields may be added within schema 1. Renaming or
  removing a field, or changing what one means, takes schema 2.

```jsonc
{
  "schema": 1,
  "host": "pi-b",                        // networking.hostName
  "role": "gateway",                     // "exit" or "gateway"
  "time": "2026-11-13T20:40:00Z",        // the Pi's clock, UTC; for information only
  "uptime_s": 187200,
  "cpu": {
    "load": [0.42, 0.31, 0.22],          // 1, 5 and 15 minutes
    "stat": {                            // the "cpu" line of /proc/stat, cumulative USER_HZ ticks;
      "user": 0, "nice": 0, "system": 0, //   the dashboard turns two readings into %
      "idle": 0, "iowait": 0, "irq": 0, "softirq": 0, "steal": 0
    }
  },
  "temp_c": 47.2,                        // SoC temperature
  "undervoltage": false,                 // the rpi_volt alarm right now; null without the sensor
  "undervoltage_events": 0,              // undervoltage seen since boot; null if it can't be read
                                         //   cheaply and without privileges
  "mem": { "total_kb": 0, "available_kb": 0 },
  "disk": { "total_b": 0, "used_b": 0, "avail_b": 0 },   // the root filesystem; avail_b as for non-root
  "net": {                               // cumulative byte counters for each of these
    "wan0": { "rx_bytes": 0, "tx_bytes": 0 }   // interfaces the Pi has: wan0, br0, tv0, tailscale0
  },
  "tunnel": [                            // a gateway: one entry, its exit node, or [] if none is set.
    {                                    // Pi A: one entry per peer tagged tag:gateway.
      "peer": "pi-a",                    // the peer's PeerStatus.HostName, so it matches that Pi's "host"
      "online": true,
      "path": "direct",                  // "direct" (CurAddr set), "peer_relay" (PeerRelay set),
                                         //   "derp" (online and active, but neither set),
                                         //   "idle" (online, not active) or "offline"
      "relay": null,                     // the DERP region, only when path is "derp"; else null
      "rx_bytes": 0, "tx_bytes": 0,      // over this peer, cumulative
      "latency_ms": 11.2                 // gateways only: one disco ping to the exit node, with a
    }                                    //   deadline of about 1 s; null on Pi A or with no answer
  ],
  "public_ip": {                         // the latest check, from a timer at most every 5 minutes,
    "value": "203.0.113.7",              //   never on the request path; null before the first one.
    "checked": "2026-11-13T20:36:00Z"    //   On a gateway with no exit node set, this is its own
  },                                     //   apartment's address. For the exit check only.
  "gateway": {                           // gateways only; null on Pi A
    "tv_port": { "link": true, "speed_mbps": 100 },
    "tv_port_gated": true,               // tv-port-gate is active, and its last check found the
                                         //   broute rule (read from a file it writes under /run)
    "fallback": false                    // ip rule 5260 is set: the TV is on its own apartment's connection
  }
}
```

## Streams

Two streams build against the contract above (`ai/09`).

| Tier | Files |
|---|---|
| Frozen | This record's contract section. A change stops both streams and goes to the user. |
| Owned by the Pi stream (branch `pi-metrics`) | `hosts/`, `modules/`, `tests/tunnel.nix`, `headscale/policy.json` |
| Owned by the dashboard stream | Its own code, wherever D3 puts it (proposed: `dashboard/` in this repository). It proposes any Pi-side or policy change here, rather than editing those files. |
| Shared, rows only added | The README status table, and the Bindings in `AGENTS.md` |
| Shared resource | The VM gate: one run at a time, under the box lock, as the Bindings say |

## Consequences

- **The Pis** get a socket unit, a per-request service and its script, a timer
  for the public address, and port 9190 opened on `tailscale0`. `tv-port-gate`
  also writes its verdict to a file. Nothing new stays running between requests.
- **Pi B's routing and firewall toward TV B don't change.** The endpoint isn't
  open on the TV's port or on either apartment's LAN.
- **Addresses still reach the dashboard privately.** They travel only over the
  tailnet to it, never onto the public page:
  - the Pis' public addresses, for the exit check;
  - the WireGuard endpoints Headscale sends to the dashboard's own tailnet node.

  The dashboard stores neither, and never exposes its tailnet status.
- **The user does these by hand:**
  - on the Headscale server: load the new policy, create a pre-auth key for
    `tag:dashboard`, and add the dashboard's routes to the reverse proxy;
  - on the cluster: the deployment, including the RBAC rule for node metrics.
- **Adding an apartment needs no change to the contract.** The dashboard learns
  the gateways from the Pis' `tunnel` lists and its tailnet view, rather than
  from a hard-coded Pi B.
- **Still open, for D3:** the dashboard's server and frontend stack, its
  storage, and where its code lives.

## Enforcement

- **`tests/tunnel.nix`**, added by the Pi stream, reads the endpoint from a VM
  that joins the tailnet as `tag:dashboard` from outside both apartments. It
  checks that:
  - every field of schema 1 is present, and the `/proc`-based fields aren't null;
  - values match their sources: `gateway.fallback` against
    `ip rule show priority 5260`, `tunnel[0].online` while Pi A is down, and
    `tv_port_gated` with the `tv-port` table removed and restored;
  - Pi B answers in under 2 seconds while Pi A is down;
  - TV B and apartment B's LAN can't reach the port;
  - the policy keeps other tailnet nodes out.

  The test logs how long one request takes.
- **The dashboard's own tests**, added by the dashboard stream, check that no
  public response contains an IP address, an endpoint or a device name.
- **Not covered by any gate:**
  - the fields only real hardware has (temperature, undervoltage, the USB
    adapter's speed);
  - the cost of one request on a Pi 3 B+.

  Both get checked on the Pis after the first deploy.
