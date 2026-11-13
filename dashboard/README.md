# Dashboard

The tunnel's public health page (`docs/decisions/D2-public-dashboard-pulls-from-the-pis.md`),
built as one Go binary (`docs/decisions/D3-dashboard-is-one-go-binary.md`).

- It joins the tunnel's tailnet with `tsnet`, for outbound requests only.
- Every 30 seconds it asks each Pi for `GET /status` on port 9190, D2's schema 1.
  - It finds the Pis in its own tailnet view: `tag:exit` is the exit node, `tag:gateway` the gateways.
  - It holds no Headscale credentials.
- It keeps 30 days of history in SQLite, through the pure-Go `modernc.org/sqlite`:
  - raw samples for 48 hours;
  - 10-minute rollups and events for 30 days.
- It renders the page on the server with `html/template`. Charts and gauges are SVG and HTML computed in Go.
- Its one script, `internal/web/static/app.js`, is hand-written. It does the colour scheme switch, the shared hover
  marker and the 30-second refresh. The page works without it.

Its only direct dependencies are `tailscale.com` and `modernc.org/sqlite`.

## What it never shows

The page never shows an IP address, a WireGuard endpoint, a device name, or the dashboard's own tailnet status.
It shows only the tagged Pis, by their host names, and the Headscale server, by that name.

- A Pi's public address is compared with the exit's as it arrives. Only whether they matched is stored.
- A failed poll shows as one of a few fixed labels, such as "timed out" or "error from the Pi". Never the
  error text.
- The tests in `internal/web` plant addresses, endpoints and names in every input and check that none reach a
  public response.

## Paths

| Path | What |
|---|---|
| `/` | The page. Its one input is `?range=1h`, `24h`, `7d` or `30d`; anything else means `24h`. |
| `/_dash/fragment?range=…` | The part of the page the script swaps in every 30 seconds. |
| `/_dash/static/…` | The stylesheet, the script and the icon, under names that carry their content's hash. |
| `/_dash/healthz` | Liveness: the process serves. |
| `/_dash/readyz` | Readiness: the database answers. The tailnet isn't required. |

Nothing is served under `/api/`, which belongs to Headscale on the same hostname.

## Running it locally

Dev mode runs the whole program with fake Pis: no tsnet, and no network but the page's own listener.
It backfills 30 days of fake history into a temporary database, so every range has something to show.
The page says "Sample data".

```sh
nix run '.#dashboard' -- -dev -listen 127.0.0.1:8080
# or, with Go 1.27.3 or newer:
cd dashboard && go run . -dev -listen 127.0.0.1:8080
```

`-dev-scenario` picks what is going on now:
- `healthy`;
- `relayed`: Pi B's tunnel relayed for the last 18 minutes;
- `fallback`: Pi A down for the last 9 minutes;
- `cutoff`: the dashboard itself cut off from the tailnet for the last 9 minutes.

`-dev-gateways` sets 1 to 3 gateways.

## Settings

Each flag can also be set from its environment variable.

| Flag | Variable | Default | What |
|---|---|---|---|
| `-control-url` | `DASH_CONTROL_URL` | none, required | The tunnel's Headscale URL |
| `-state-dir` | `DASH_STATE_DIR` | `/var/lib/dashboard` | The database, the tailnet state and temporary files. It needs a volume |
| `-listen` | `DASH_LISTEN` | `:8080` | The public HTTP address |
| `-hostname` | `DASH_HOSTNAME` | `dashboard` | This node's name on the tailnet |
| `-k8s-node` | `DASH_K8S_NODE` | none | The cluster node hosting Headscale. Without it, or outside a cluster, its gauges show "no data" |
| `-tz` | `DASH_TZ` | `UTC` | The time zone the page shows times in |
| `-dev` | `DASH_DEV` | `false` | Dev mode, above |
| | `TS_AUTHKEY` | none | The pre-auth key, read only for the first login |

## Building

```sh
nix build '.#dashboard'          # the binary, after gofmt, go vet and the Go tests
nix build '.#dashboard-image'    # the OCI image, as a docker-archive tarball in ./result
docker load < result             # or: podman load < result
```

- **The image** is amd64 only, as the cluster's nodes are.
  - It runs as uid 65532, and keeps its state in the volume at `/var/lib/dashboard`.
  - It carries a CA bundle for TLS to Headscale.
  - The user pushes it. Agents don't.
- **`checks.x86_64-linux.dashboard`** builds the same thing as `.#dashboard`. On the shared build machine, run
  it under the box's lock, as `private/gate.md` describes for the VM test.
- **`tailscale.com` in `go.mod` must equal nixpkgs' `tailscale` version**, which is what the Pis run. The
  build refuses otherwise.
  - So `tailscale.com` moves only after a `flake.lock` update.
  - `go.mod`, `go.sum` and the vendor hash in `vendor-hash.nix` change together, in a commit of their own.

## Deploying

The user deploys it. These are the pieces the cluster needs:

- **One replica, with the `Recreate` strategy.** The program locks its state directory at start, and a second
  instance exits rather than share the tailnet node and the database.
- **One volume** at `/var/lib/dashboard`, writable by uid 65532: set `fsGroup: 65532`.
- **The pre-auth key**, as `TS_AUTHKEY` from a Secret.
  - Use a single-use key for `tag:dashboard` that expires soon. The tag comes from the key, as it does for the
    Pis. The program doesn't ask for one.
  - The node's state on the volume keeps it logged in after that, so the Secret can be deleted after the
    first login.
  - If the node isn't tagged `tag:dashboard`, the program logs an error and the page says the dashboard isn't
    set up.
- **Read-only RBAC for the node metrics.** Bind a ClusterRole like this one to the pod's service account. It
  takes two reads, because the metrics API only reports usage, and the capacity comes from the node:

  ```yaml
  rules:
    - apiGroups: [""]
      resources: ["nodes"]
      resourceNames: ["<node-name>"]
      verbs: ["get"]
    - apiGroups: ["metrics.k8s.io"]
      resources: ["nodes"]
      resourceNames: ["<node-name>"]
      verbs: ["get"]
  ```
- **Probes:** liveness on `/_dash/healthz`, readiness on `/_dash/readyz`.
- **The reverse proxy** in front of Headscale forwards only `/`, exactly, and `/_dash/` to the dashboard. It
  keeps everything else, `/api/` included, for Headscale.

## When the dashboard itself is cut off

When its own node isn't running, isn't in contact with the control server for 2 minutes, or isn't tagged, the
dashboard stops polling.

- The page then says that the dashboard can't see the tailnet, and shows the last data it had, dimmed.
- It never shows that as the tunnel being down.
- The history keeps a gap for that time, and the events say when the dashboard lost the tailnet and got it
  back.
- The Headscale card's CPU and memory stay live, as they come from the cluster.

## Not covered by its tests

The tests cover:
- the parsing;
- sampling and storage, read back from a real SQLite file;
- the banner, events and checks;
- the routes;
- privacy.

They don't cover:
- **tsnet against a real Headscale:** joining, the tag, and dialling a Pi over the tailnet;
- **the cluster's real metrics API and RBAC;**
- **the real Pis' endpoint.** Its schema is tested against D2's contract and the example in it, not against the Pis.
