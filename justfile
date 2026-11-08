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
