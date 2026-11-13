# The dashboard's binary (D3), built without cgo. Its checkPhase is the
# dashboard's gate: gofmt, go vet and the Go tests.
{
  lib,
  buildGoModule,
  go_latest,
  tailscale,
}:

let
  # tailscale.com in go.mod moves only with nixpkgs' tailscale, which is what
  # the Pis run, so Headscale already accepts it. A flake.lock bump that
  # changes it fails here until go.mod follows, in a commit of its own.
  goMod = builtins.readFile ./go.mod;
  found = builtins.match ".*\n[[:space:]]*tailscale\\.com v([^[:space:]]+)[[:space:]].*" goMod;
  tailscaleInGoMod =
    if found == null then throw "dashboard/go.mod doesn't require tailscale.com" else builtins.head found;
in
assert lib.assertMsg (tailscaleInGoMod == tailscale.version)
  "dashboard/go.mod requires tailscale.com v${tailscaleInGoMod}, but nixpkgs has tailscale ${tailscale.version}. Move go.mod to match, in its own commit.";

# tailscale.com needs a newer Go than nixpkgs' default.
(buildGoModule.override { go = go_latest; }) {
  pname = "raspberry-tunnel-dashboard";
  version = "0.1.0";

  src = lib.fileset.toSource {
    root = ./.;
    fileset = lib.fileset.unions [
      ./go.mod
      ./go.sum
      (lib.fileset.fileFilter (
        f: lib.any f.hasExt [ "go" "html" "css" "js" "svg" "woff2" "txt" ]
      ) ./.)
    ];
  };

  vendorHash = import ./vendor-hash.nix;

  env.CGO_ENABLED = "0";
  ldflags = [
    "-s"
    "-w"
  ];

  preCheck = ''
    unformatted=$(gofmt -l $(find . -name '*.go' -not -path './vendor/*'))
    if [ -n "$unformatted" ]; then
      echo "These files need gofmt:" $unformatted >&2
      exit 1
    fi
    go vet ./...
  '';

  meta = {
    description = "Public health page for the raspberry-tunnel Pis";
    license = lib.licenses.mit;
    mainProgram = "dashboard";
    platforms = [ "x86_64-linux" ];
  };
}
