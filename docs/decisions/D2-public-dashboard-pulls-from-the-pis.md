# D2: A public dashboard shows the tunnel's live health, pulled from the Pis over the tailnet

- **Date:** 2026-11-13
- **Status:** accepted
- **Decided by:** the user, who chose:
  - public and live data, with no sign-in;
  - the design (mockup A);
  - the user's k3s cluster as the host;
  - the Pi side with the least performance impact.

  The agent proposed pulling over the tailnet, the endpoint's shape and the
  contract below. The dashboard's own stack and hostname are left to D3.

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
- The page is public. It must not reveal anyone's addresses.

The options for getting the numbers to the dashboard:

- **The Pis push** to a public endpoint. Pi B's own traffic goes through Pi A as
  its exit node, and Tailscale drops it while Pi A can't be reached. So Pi B
  would go silent exactly when the tunnel fails, unless its routing gets an
  exception.
- **The dashboard joins the tailnet and pulls.** Traffic between two tailnet
  nodes doesn't go through the exit node. So Pi B stays reachable while Pi A is
  down, and Pi B's routing doesn't change. Chosen.

The options on the Pis:

- **Prometheus `node_exporter`**, plus a timer that writes the tunnel-specific
  numbers to files for it. It's one more process resident all the time, and the
  timer runs whether or not anyone is reading.
- **A socket-activated endpoint.** Nothing runs between requests, and each
  request reads what it needs once. Chosen, because the user asked for whatever
  affects the Pis' performance least.

## Decision

1. **Public and live.** Anyone can open the dashboard, and it shows real data as
   it is collected, with no sign-in. The sign-in design (passkey first, then
   email and password) is shelved, not dropped.
2. **The design is mockup A, the overview.**
   - **The header** has the name "raspberry tunnel", with "raspberry" in berry
     red, and a logo of a raspberry drawn as a mesh of connected nodes. Beside it
     are:
     - a 1 h, 24 h, 7 d or 30 d range switch;
     - a colour scheme switch. It follows the system's light or dark setting
       until the viewer picks one.
     - an icon-only GitHub link to this repository.
   - **A status banner** says what's wrong, if anything, and how much of the
     range each TV spent on the tunnel.
   - **A diagram of the traffic paths.** It has one row per gateway apartment
     (TV, then its Pi, then its tunnel), and the tunnels all converge on Pi A,
     then the internet. Headscale sits above, marked as carrying no traffic.
   - **One card per Pi, all with the same fields.**
     - Liquid-fill circle gauges: tunnel traffic in a large one, plus
       temperature, CPU, memory and SD card.
     - Each value sits inside its circle. It is drawn twice, clipped to the
       liquid and to the empty part, each copy coloured with CSS
       `contrast-color()` and a fixed fallback.
     - Below the gauges: role, uptime, power and last report.
   - **A Headscale card**, with gauges for the server's CPU and memory, and the
     nodes online, last check-ins, node keys and policy.
   - **An exit route strip** with one row per TV, showing tunnel or own
     connection over time.
   - **Line charts** for tunnel traffic, tunnel latency, temperature (with a
     line at 60 °C, where the Pi lowers its clock) and CPU. Hovering one moves a
     marker across all of them.
   - **Lists of events and checks.**

   Apartments are named after their Pi, and Pi A is the only exit.
3. **The dashboard runs on the user's k3s cluster.**
   - It joins the tunnel's tailnet as its own node, tagged `tag:dashboard`.
   - It asks every Pi for its status every 30 seconds and keeps 30 days in
     SQLite.
   - It reads Headscale's view (nodes online, last seen, key expiry) from the
     Headscale server.
   - The user deploys it from their own cluster repository. Agents never
     touch the cluster.
4. **Each Pi serves the read-only endpoint in the contract below.** It is
   socket-activated, so nothing runs between requests. The firewall opens it on
   `tailscale0` only.
5. **The Headscale policy lets in only `tag:dashboard` and the user's own
   devices (`tunnel@`),** on that port only. The user's devices can read it by
   hand, and the VM test reads it from the user-owned phone it already has.
6. **No addresses on the public page.**
   - The dashboard never publishes an IP address, a WireGuard endpoint, or
     anything else that identifies a connection.
   - The Pis send their public address only for the exit check. The dashboard
     compares them and publishes only whether they match.

## Contract: the Pi status endpoint, schema 1

The Pi stream builds this, and the dashboard stream reads it (`ai/09`). Neither
changes it without the other, and a change goes through this record.

- **Transport.** `GET /status` over HTTP on TCP port 9190, at the Pi's tailnet
  address.
  - Any other path gets a 404.
  - The answer is `application/json`, one object, built fresh for each request.
  - It should arrive within 2 seconds.
  - The connection closes after the reply.
- **On failure.** If a value can't be read, its field is `null`. It is never
  left out and never replaced by a made-up number. If building the answer fails
  as a whole, the reply is HTTP 500 with `{"schema": 1, "error": "<what failed>"}`.
- **Compatibility.** New fields may be added within schema 1. Renaming or
  removing a field, or changing what one means, takes schema 2.

```jsonc
{
  "schema": 1,
  "host": "pi-b",                        // networking.hostName
  "role": "gateway",                     // "exit" or "gateway"
  "time": "2026-11-13T20:40:00Z",        // when this answer was built, UTC
  "uptime_s": 187200,
  "cpu": {
    "load": [0.42, 0.31, 0.22],          // 1, 5 and 15 minutes
    "stat": {                            // the "cpu" line of /proc/stat, cumulative;
      "user": 0, "nice": 0, "system": 0, //   the dashboard turns two readings into %
      "idle": 0, "iowait": 0, "irq": 0, "softirq": 0, "steal": 0
    }
  },
  "temp_c": 47.2,                        // SoC temperature
  "undervoltage": false,                 // the rpi_volt alarm right now; null without the sensor
  "mem": { "total_kb": 0, "available_kb": 0 },
  "disk": { "total_b": 0, "used_b": 0 }, // the root filesystem
  "net": {                               // cumulative byte counters for each of these
    "wan0": { "rx_bytes": 0, "tx_bytes": 0 }   // interfaces the Pi has: wan0, br0, tv0, tailscale0
  },
  "tunnel": [                            // a gateway: one entry, its exit node.
    {                                    // Pi A: one entry per peer tagged tag:gateway.
      "peer": "pi-a",
      "online": true,
      "path": "direct",                  // "direct", "relay" or "none"
      "relay": "fra",                    // the DERP region while relayed, else null
      "rx_bytes": 0, "tx_bytes": 0,      // over this peer, cumulative
      "latency_ms": 11.2                 // gateways only: a disco ping to the exit node;
    }                                    //   null on Pi A or with no answer
  ],
  "public_ip": {                         // the latest check, run at most every 5 minutes and
    "value": "203.0.113.7",              //   cached; null before the first one. For the exit
    "checked": "2026-11-13T20:36:00Z"    //   check only: the dashboard never publishes it
  },
  "gateway": {                           // gateways only; null on Pi A
    "tv_port": { "link": true, "speed_mbps": 100 },
    "tv_port_gated": true,               // the tv-port table is loaded and tv-port-gate is active
    "fallback": false                    // ip rule 5260 is set: the TV is on its own apartment's connection
  }
}
```

## Consequences

- **The Pis** get a socket unit, a per-request service and its script, and port
  9190 opened on `tailscale0`. Nothing new stays running. Each request reads
  `/proc` and `/sys` and asks tailscaled's local API, and on a gateway it also
  sends one disco ping. The work that adds this measures and reports what one
  request costs on the Pi.
- **Pi B's routing and firewall toward TV B don't change.** The endpoint isn't
  open on the TV's port or on either apartment's LAN.
- **Apartment A's public address is the one value that leaves the Pi and must
  stay out of public view.** It travels only over the tailnet to the dashboard,
  which keeps it out of every public response.
- **The user does these by hand:** on the Headscale server, load the new policy
  and create a pre-auth key for `tag:dashboard`; on the cluster, the
  deployment.
- **Adding an apartment needs no change to the contract.** The dashboard learns
  the gateways from the Pis' `tunnel` lists and Headscale, rather than from a
  hard-coded Pi B.
- **Still open, for D3:** the dashboard's server and frontend stack, and its
  hostname (the real names are in `private/network.md`).

## Enforcement

- **`tests/tunnel.nix`**, added by the Pi stream, checks that:
  - each Pi's endpoint answers over the tailnet with schema 1 and every field
    above;
  - TV B and apartment B's LAN can't reach it;
  - Pi B still answers while Pi A is down;
  - the policy keeps every other tailnet node out.
- **The dashboard's own tests**, added by the dashboard stream, check that no
  public response contains an IP address.
