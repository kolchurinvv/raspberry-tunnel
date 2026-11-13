# D3: The dashboard is one Go binary that joins the tailnet itself and renders its pages on the server

- **Date:** 2026-11-13
- **Status:** accepted; amends D2, settling what D2 left open
- **Decided by:** the user.
  - The dashboard is only a foil for the project. What the repository showcases
    is the NixOS configuration and the tunnel, so the dashboard should take the
    least effort to maintain.
  - Joining the tailnet without a sidecar was worth having.
  - The user accepted this stack and where its code lives.

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
2. **It joins the tunnel's tailnet with `tsnet`.**
   - It uses the tunnel's Headscale as its control server and is tagged
     `tag:dashboard`.
   - Its tailnet state lives on the same volume as its database, so a restarted
     pod comes back as the same node.
   - The pre-auth key is needed only for the first login.
3. **It finds the Pis from its tailnet view,** with no list of hosts in its
   configuration.
   - The exit node is the peer tagged `tag:exit`. Gateways are the peers tagged
     `tag:gateway`.
   - It asks each one for `GET /status` (D2's contract) every 30 seconds.
   - It takes each node's online state, last-seen time and key expiry from the
     same view. It holds no Headscale credentials.
4. **The cluster node hosting Headscale.** Its CPU and memory come from the
   cluster's `metrics.k8s.io` API.
   - The request is plain HTTPS, authenticated with the pod's service-account
     token. No Kubernetes client library.
   - The node's name comes from the configuration.
   - When the API isn't available, as outside the cluster, those gauges show "no
     data". That isn't an error.
5. **Pages are rendered on the server.**
   - Go's standard `html/template`, with the templates, CSS and JavaScript built
     into the binary through `embed`.
   - Charts and gauges are SVG and HTML computed in Go, as the mockup computes
     them in JavaScript.
6. **The JavaScript is hand-written, with no build step, no `package.json` and
   no third-party scripts.** It does only three things:
   - the shared hover marker on the charts, read from data the server puts in
     the page;
   - the colour scheme switch, which follows the system until the viewer picks;
   - swapping in fresh server-rendered HTML every 30 seconds.

   The page works without JavaScript; it just doesn't refresh or hover.
7. **Storage is SQLite, through `modernc.org/sqlite`,** a pure-Go driver, so the
   build needs no C compiler.
   - It keeps 30 days of history.
   - The program chooses how to thin out older samples, as long as the 30-day
     view stays quick.
   - It stores neither public addresses nor endpoints, only whether the exit
     check matched (D2).
8. **Public paths.** The program serves the page at `/` and everything else
   under `/_dash/`. It never uses `/api/`, which belongs to Headscale on the same
   hostname (D2).
   - The only input it accepts is the range: `1h`, `24h`, `7d` or `30d`.
     Anything else falls back to `24h`.
9. **Dependencies are limited to `tailscale.com` and `modernc.org/sqlite`,** plus
   whatever those pull in. Adding another takes a change to this record.
10. **This repository's flake builds it:**
    - the binary;
    - an OCI image for the cluster;
    - a check that runs the Go tests.

    The user pushes the image and deploys it. Agents don't.

## Consequences

- **`go.mod`, `go.sum` and the Nix vendor hash** become generated baselines
  under rule 3. They change only when the dependencies change, in a commit of
  their own whose message says so.
- **Keeping the dashboard working** means bumping `tailscale.com` now and then,
  so that Headscale keeps accepting the client. Nothing else needs regular
  attention.
- **The cluster deployment needs no sidecar:**
  - one container;
  - one volume, holding the database and the tailnet state;
  - one Secret, for the first-login key;
  - read-only RBAC for node metrics.
- **The page is plain server-rendered HTML,** not a single-page app. Nothing
  here needs more.
- **Development and tests use a fake Pi.** A small test server answers
  `/status` with D2's schema, so the dashboard can be built and tested without
  the Pis or a tailnet.
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

- **The Go tests,** run by the flake check:
  - the parsing of D2's schema 1, including null fields and counters that drop
    on a reset;
  - the banner and event logic from D2;
  - the range input;
  - a check that no public response contains an IPv4 or IPv6 address, an
    endpoint, or a name other than the Pis' hostnames;
  - a check that every public route lives at `/` or under `/_dash/`, never
    under `/api/`.
- **The dependency rule** is held by `go.mod`. A new requirement shows up in
  review as a change to it.
- **Joining the VM test** is to come, after the Pi stream lands, as described
  above.
