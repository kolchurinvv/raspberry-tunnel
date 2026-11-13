# D3: The dashboard is one Go binary that joins the tailnet itself and renders its pages on the server

- **Date:** 2026-11-13
- **Status:** accepted; amends D2. It settles what D2 left open, and records what the two streams and their reviews settled about D2's contract and banner.
- **Decided by:** the user.
  - The dashboard is only a foil for the project. What the repository showcases
    is the NixOS configuration and the tunnel, so the dashboard should take the
    least effort to maintain.
  - Joining the tailnet without a sidecar was worth having.
  - The user accepted this stack and where its code lives.
  - The user ruled on 2026-11-13, after the reviews:
    - amd64 only;
    - how the dependency files change (Consequences);
    - the dashboard's check, run under the box lock;
    - the banner levels in "What the page says";
    - the cut-off state;
    - the privacy and route tests becoming a ratchet;
    - a lighter sandbox for the Pis' endpoint;
    - a second site for the exit check.

  The agent laid out the options and proposed the details below.

## Context

D2 fixed what the dashboard shows, where it runs, and what each Pi serves. It
left these open:
- the stack;
- the storage;
- where the code lives.

Two things narrowed the choice:
- **Upkeep.** Each dependency, build step and package manager is something to
  update forever.
- **Joining the tailnet.** The dashboard must join the tunnel's tailnet to reach
  the Pis. Only Tailscale's Go library, `tsnet`, does that from inside a program.
  - Other languages either wrap that same Go library through the experimental C
    wrapper `libtailscale`, or need a Tailscale sidecar container in the pod.
  - WebAssembly builds of Tailscale run only in a browser, which would put the
    tailnet key on a public page.

The options weighed:
- **Go with `tsnet`.** One binary that joins by itself.
- **SvelteKit or another JavaScript framework**, with a sidecar. Two containers,
  plus npm upkeep.
- **Elixir/Phoenix or Rust.** Either a sidecar, or Go hidden behind a C wrapper.

## Decision

1. **One Go program,** in `dashboard/` in this repository, as module
   `github.com/kolchurinvv/raspberry-tunnel/dashboard`.
2. **It joins the tunnel's tailnet with `tsnet`, for outbound connections only.**
   - It uses the tunnel's Headscale as its control server.
   - It never listens on the tailnet, and runs no web client. Its public page
     is a separate, ordinary TCP listener that starts before tsnet does.
   - **Tailnet state:** tsnet's state directory is set explicitly, on the same
     volume as the database, so a restarted pod comes back as the same node.
   - **Logs:** upload to Tailscale's log service is off from the start.
   - **Tag:**
     - Its tag comes from its pre-auth key. It doesn't ask for tags itself.
     - The key is single-use and short-lived, and is needed only for the first
       login.
     - If its own node lacks `tag:dashboard`, it logs an error and the page says
       it isn't configured.
   - **One copy at a time:** it locks its data directory at start, so a second
     copy exits. The deployment uses the Recreate strategy.
3. **It finds the Pis from its tailnet view,** with no list of hosts in its
   configuration.
   - The exit node is the peer tagged `tag:exit`. Gateways are the peers tagged
     `tag:gateway`.
   - Several peers can share a tag, for example a stale node left by a re-flash.
     The order is fixed: online first, then latest seen.
   - It asks each one for `GET /status` (D2's contract) every 30 seconds, with a
     3-second timeout. It never follows a redirect.
   - It takes each node's online state, last-seen time and key expiry from the
     same view. It holds no Headscale API key.
4. **The cluster node hosting Headscale.** Its CPU and memory take two reads,
   over plain HTTPS, with the pod's service-account token read fresh each time.
   No Kubernetes client library.
   - **Usage** comes from `metrics.k8s.io`.
   - **Capacity** comes from the core API's node object.
   - **RBAC:** `get` on `nodes` in both API groups, and nothing else.
   - **Configuration:** the node's name.
   - **Outside the cluster** those gauges show "no data". That isn't an error.
5. **Pages are rendered on the server.**
   - Go's standard `html/template`, with the templates, CSS, JavaScript and the
     Inter typeface built into the binary through `embed`. Inter is under the
     OFL licence, included alongside it.
   - Charts and gauges are SVG and HTML computed in Go.
   - Each range's page is cached per 30-second tick.
   - A strict content security policy loads nothing from anywhere else.
6. **The JavaScript is hand-written, with no build step, no `package.json` and
   no third-party scripts.** It does only three things:
   - the shared hover marker on the charts, read from data the server puts in
     the page;
   - the colour scheme switch, which follows the system until the viewer picks;
   - swapping in fresh server-rendered HTML every 30 seconds.

   The page works without JavaScript; it just doesn't refresh or hover.
7. **Storage is SQLite, through `modernc.org/sqlite`,** a pure-Go driver, so the
   build needs no C compiler.
   - **Retention:** raw samples for 48 hours, then 10-minute rollups for 31 days.
     The 30-day view stays quick.
   - **No addresses or endpoints are stored.** For the exit check, only whether
     it matched, and only while both Pis' last successful checks are under 10
     minutes old.
8. **Public paths.** The program serves the page at `/` and everything else
   under `/_dash/`, favicon included. It never uses `/api/`, which belongs to
   Headscale on the same hostname (D2).
   - The root route matches `/` exactly. Every other path is a 404.
   - The only input it accepts is the range: `1h`, `24h`, `7d` or `30d`.
     Anything else falls back to `24h`.
   - The page shows failures only as fixed labels, never as error text or text
     a Pi sent.
9. **Dependencies are limited to `tailscale.com` and `modernc.org/sqlite`,** plus
   whatever those pull in. Adding another takes a change to this record.
10. **This repository's flake builds it, for amd64 only:**
    - the binary, with the pinned nixpkgs' newest Go (`go_latest`);
    - an OCI image that runs as a non-root user (65532), with a CA bundle and a
      writable `/tmp`;
    - a check that runs gofmt, go vet and the Go tests.

    Evaluation fails if `go.mod`'s `tailscale.com` version differs from the
    pinned nixpkgs' `tailscale`. The user pushes the image and deploys it.
    Agents don't.

## What the page says

D2's banner rule now reads as below.

| Level | When |
|---|---|
| **Unknown** (grey, never red) | The dashboard itself can't see the tailnet, or isn't tagged. The page says so, shows the last data it had dimmed, and blames the dashboard, not the tunnel. The Headscale server's gauges stay live, because they come from the cluster. |
| **Critical** | The exit node can't be reached. Or a gateway answers but its tunnel is down: `path` `offline`, `tunnel` `[]` (no exit node set), or `tunnel` `null` (tailscaled not running). Or any gateway's `fallback` is set. |
| **Warning** | Any tunnel is relayed, or the exit check doesn't match, or any Pi shows undervoltage or is at 60 °C or above. Also when a gateway doesn't answer the dashboard at all, or its path or fallback state can't be read. The page never says "All good" when it can't know. |
| **No alarm** | A TV's port is down, which is usually the TV being off. The exit-route strip gets a fifth state, "TV off", and the gateway's card notes it. |
| **Good** | Otherwise. The "All good" text is built from the data. It never claims "direct", a latency, or steady power the data doesn't show. |

- **Power** is judged from the live undervoltage alarm, because
  `undervoltage_events` can't be read on these kernels (below).
- **The dashboard's own state appears on the page:** the grey banner, events such
  as "Dashboard · Lost the tailnet", and "isn't tagged". This amends D2 §7. Its
  own addresses, names and tailnet status still never appear.

## What the Pi side settled about D2's contract

These change nothing in schema 1. They pin down meanings the two streams found
had to be explicit:

- **`public_ip.checked`** is the time of the last *successful* check. A failed
  check leaves both value and time unchanged, so a check older than about 10
  minutes counts as unknown.
- **`undervoltage_events`** is always `null`. NixOS kernels restrict the kernel
  log, and the hwmon driver keeps no count.
- **`gateway.tv_port.link`:**
  - `false` when the adapter is absent;
  - `null` when its state is unknown.
- **`net`:** an interface the Pi lacks is listed with null counters.
- **`path`:** a peer with a current address reads `direct` even while idle.
- **`tunnel`:** `null` while tailscaled can't be reached or isn't Running.
- **A request that doesn't arrive within half a second** gets HTTP 408.
- **The exit check tries a list of sites** (`tunnel.publicIpUrls`): icanhazip.com
  first, then Amazon's checkip.amazonaws.com, a different provider, as the
  user's chosen fallback. Both see the Pis' public addresses.
- **The endpoint runs as a fixed system user,** with no capabilities, a
  system-call filter, and no per-request namespaces. This was the user's ruling,
  trading some isolation for less CPU per request on a Pi 3 B+. It stays
  unprivileged, as D2 §5 requires.

## Consequences

- **The dependency files.** `dashboard/go.mod`, `dashboard/go.sum` and
  `dashboard/vendor-hash.nix` change only in a dependency commit of their own:
  - `tailscale.com` moves only right after a `flake.lock` bump the user asked
    for;
  - any other version bump only when the user asks;
  - a new direct dependency only with an amendment to this record.

  The Bindings carry this as a rule.
- **Keeping it working** means bumping `tailscale.com` together with `flake.lock`
  now and then, so that Headscale keeps accepting the client. Both are the
  user's call. Nothing else needs regular attention.
- **The cluster deployment needs no sidecar:**
  - one container, using the Recreate strategy;
  - one volume, holding the database and the tailnet state;
  - one Secret, for the first-login key, which can go once it's used;
  - read-only RBAC for the two node reads;
  - `DASH_TZ` set to the site's time zone.
- **The page is plain server-rendered HTML,** not a single-page app. Nothing
  here needs more.
- **Development and tests use a fake Pi and a fake tailnet view.** The dashboard
  is built and tested without the Pis or a tailnet.
- **Later, the dashboard joins the VM test.** Once the Pi stream lands, the VM
  test's `tag:dashboard` node can run the real binary. The test then exercises
  the whole chain: the dashboard, the tunnel's Headscale, and both Pis'
  endpoints.

## Streams

D2's Streams table still holds, with the dashboard stream's files now named:

| Tier | Files |
|---|---|
| Owned by the dashboard stream (branch `dashboard`) | `dashboard/`, and the flake outputs for it |
| Shared, rows only added | `flake.nix`, for the dashboard stream's new package and check outputs; the README status table; the Bindings in `AGENTS.md` |
| Not touched by the dashboard stream | `hosts/`, `modules/`, `tests/tunnel.nix`, `headscale/policy.json`. The Pi stream owns them until it lands. Joining the VM test comes after. |

## Enforcement

- **`checks.x86_64-linux.dashboard`** (gofmt, go vet, the Go tests) is the gate
  for changes under `dashboard/` and to `flake.nix`. It runs under the box lock.
  The tests cover:
  - the parsing of D2's schema 1, including nulls, resets and odd host names;
  - the banner, captions and events;
  - the range input;
  - the rollups, read back from a real SQLite file.
- **The privacy and route tests can only get stricter** (the user's ruling).
  - `TestPrivacy` plants tailnet and public addresses, endpoints, the MagicDNS
    domain, foreign names and Pi error text, and checks that none reaches any
    public response.
  - `TestNoAddressStored` checks the database file holds none of them.
  - `TestRoutes` checks every public path is `/` or under `/_dash/`.
- **The flake's version assertion** keeps `tailscale.com` matched to the pinned
  nixpkgs.
- **The dependency rule** is held by `go.mod`. A new requirement shows up in
  review as a change to it.
- **Joining the VM test** is to come, after the Pi stream lands, as described
  above.
