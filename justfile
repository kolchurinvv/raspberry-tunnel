# .env holds PI_A_PUB and PI_B_PUB, the absolute paths to each Pi's SSH public key.
set dotenv-load

# The real ranges for the firewall's block list, in the gitignored private folder.
extra_local_nets := quote("path:" + justfile_directory() + "/private/extra-local-nets.nix")

default:
  @just --list

# Build a Pi's config and install it for its next boot, with that Pi's own key
[arg("pi", pattern="a|b")]
update-nix-on pi: (check-key pi)
  nixos-rebuild boot --flake '.#pi-{{ pi }}' --target-host rt-pi-{{ pi }} --no-reexec --override-input admin-key {{ quote("path:" + env("PI_" + uppercase(pi) + "_PUB")) }} --override-input extra-local-nets {{ extra_local_nets }}

# Reboot a Pi (ssh exits with an error as the Pi drops the connection)
[arg("pi", pattern="a|b")]
reboot-pi pi:
  -ssh rt-pi-{{ pi }} reboot

# Build a Pi's SD card image, with that Pi's own key, into ./result
[arg("pi", pattern="a|b")]
build-image pi: (check-key pi)
  nix build '.#nixosConfigurations.pi-{{ pi }}.config.system.build.sdImage' --override-input admin-key {{ quote("path:" + env("PI_" + uppercase(pi) + "_PUB")) }} --override-input extra-local-nets {{ extra_local_nets }}

# Where the cluster pulls the dashboard's image from.
dashboard_image := "ghcr.io/kolchurinvv/raspberry-tunnel-dashboard"

# Run it from main/, on a commit that has reached GitHub, with gh logged in (its
# token needs write:packages). On the shared build machine, run it when the box is
# free: it builds the dashboard if the store doesn't have it yet.
# Build the dashboard's image and push it to ghcr.io, tagged with this commit and latest
push-dashboard-image:
  #!/usr/bin/env bash
  set -euo pipefail
  fail() { echo "error: $*" >&2; exit 1; }
  image={{ quote(dashboard_image) }}
  # The tag must name exactly what's in the image, and a commit anyone can look up.
  [ -z "$(git status --porcelain)" ] || fail "the tree has uncommitted changes, so the tag wouldn't match the image"
  git fetch --quiet origin main
  git merge-base --is-ancestor HEAD origin/main || fail "HEAD isn't on origin/main: push images only from merged commits"
  rev=$(git rev-parse --short=12 HEAD)
  archive=$(nix build '.#dashboard-image' --no-link --print-out-paths)
  # skopeo from this flake's own nixpkgs, so nothing depends on a Docker daemon.
  skopeo() { nix run --inputs-from . nixpkgs#skopeo -- "$@"; }
  # Log in through a throwaway auth file: the token goes in on stdin, never on a
  # command line, and nothing is left behind afterwards.
  export REGISTRY_AUTH_FILE
  REGISTRY_AUTH_FILE=$(mktemp)
  trap 'rm -f "$REGISTRY_AUTH_FILE"' EXIT
  gh auth token | skopeo login --username "$(gh api user --jq .login)" --password-stdin ghcr.io
  for tag in "$rev" latest; do
    skopeo copy "docker-archive:$archive" "docker://$image:$tag"
  done
  digest=$(skopeo inspect --format '{{"{{"}}.Digest{{"}}"}}' "docker://$image:$rev")
  echo "pushed $image:$rev and :latest"
  echo "to pin it in the cluster: $image@$digest"

# Refuse a key that isn't the one ~/.ssh/config logs in to the Pi with: installing
# it would lock you out at the Pi's next boot
[private]
[arg("pi", pattern="a|b")]
check-key pi:
  #!/usr/bin/env bash
  set -euo pipefail
  key={{ quote(env("PI_" + uppercase(pi) + "_PUB")) }}
  identity=$(ssh -G rt-pi-{{ pi }} 2> /dev/null | awk '$1 == "identityfile" { print $2; exit }')
  identity="${identity/#\~/$HOME}"
  fail() { echo "error: $*" >&2; exit 1; }
  [ -f "$key" ] || fail "$key is not a file"
  [ -f "$identity.pub" ] || fail "no $identity.pub next to the key ssh uses for rt-pi-{{ pi }}"
  ssh-keygen -lf "$key" > /dev/null 2>&1 || fail "$key is not an SSH public key"
  have=$(awk '{ print $2 }' "$key")
  want=$(awk '{ print $2 }' "$identity.pub")
  [ -n "$have" ] && [ "$have" = "$want" ] || fail "$key is not the key ssh uses for rt-pi-{{ pi }} ($identity.pub)"
