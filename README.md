# raspberry-tunnel

Two Raspberry Pi 3 B+ boards running NixOS, so the TV in apartment B uses
apartment A's internet connection, while staying on its own apartment's network
for casting.

```
Apartment A                                Apartment B

TV A ─ router ── ISP router ── internet ── combo unit ── Pi B ── USB adapter ── TV B
          │
         Pi A (exit)
```

Each apartment is named after its Pi: apartment A has Pi A and TV A, apartment B
has Pi B and TV B, and any later apartment takes its Pi's letter.

Pi B bridges TV B onto apartment B's network, so the TV gets its address from the
combo unit and phones there can cast to it as usual. Everything TV B sends to the
internet still goes through Pi B, over the tunnel to Pi A, and out through
apartment A's connection. If the tunnel stops working, Pi B moves TV B to
apartment B's own connection after about 20 seconds, so the TV stays online but
shows apartment B's address. It moves back about 30 seconds after the tunnel
returns. If apartment B's connection is down as well, the TV stays where it is, as
moving wouldn't help. Pi B being off also takes TV B offline, as the TV has no
other path. A Headscale server on a small VPS connects the Pis and can relay
between them, but traffic never exits there.

## Status

| Piece | State |
|---|---|
| Pi A config (`hosts/pi-a.nix`, `modules/`) | Passes the VM test. Runs on a real Pi, has joined Headscale, and serves as Pi B's exit node |
| Pi B config (`hosts/pi-b.nix`, `modules/`) | Passes the VM test. Runs on a real Pi in apartment B, with TV B bridged onto the combo unit's network through its USB adapter. TV B's traffic goes through the tunnel, which reached Pi A over a direct path rather than a relay, and websites see apartment A's address. Apartment B's network hands out IPv6, and TV B gets none. Casting from a phone on apartment B's Wi-Fi works. Before moving, it was tried in apartment A on the default VLAN, where streaming, casting from an app and screen mirroring all worked, on Pi B's own connection and through the tunnel |
| Headscale policy (`headscale/policy.json`) | Passes the VM test |
| Headscale server | Runs as a second Headscale on an existing server, set up by hand. Its config isn't in this repository |
| SD card images | Both built and flashed |
| Waking the workstation from the phone through Pi A | Not started, secondary to the tunnel |

## Testing

```sh
nix build '.#checks.x86_64-linux.tunnel' -L --no-link
```

This needs x86_64 Linux with KVM and about 8 GB of free memory. It boots the real Pi
roles and a real Headscale in VMs, then checks where TV B's traffic goes and what it
can't reach, and that a phone on apartment B's network can find TV B and connect
to it. It also checks the move to apartment B's connection and back when the
tunnel fails, and recovery after a power cut, the TV adapter being replugged, and
`systemd-networkd` restarting.

It doesn't cover connecting through CGNAT, the ARM images, the Pi's network hardware
or USB adapter, real speed, or real casting apps.

## First-time setup

All the steps below have been run on real hardware. Pi B was set up in
apartment A, running steps 1 to 5 before it bridged its TV port, then moved to
apartment B, where step 9 passed. Passing
`private/extra-local-nets.nix` to a build is newer, and has only been evaluated.

1. Give each Pi an SSH key pair of its own, and keep the keys outside this repository,
   which may become public. Every Pi build takes its Pi's public key on the command
   line and refuses to run without one:
   `--override-input admin-key path:/absolute/path/to/key.pub`. Each build replaces
   the key NixOS lets root log in with, so giving the wrong Pi's key locks you out
   of that Pi as soon as the new build is active: at once with `switch`, after the
   reboot with `boot`. The justfile's recipes check the key for you: put the paths in
   a `.env`, which git ignores, as `PI_A_PUB=/…/pi-a.pub` and `PI_B_PUB=/…/pi-b.pub`.
   They refuse a key that isn't the one your `~/.ssh/config` logs in to that Pi with,
   and expect that file to define `rt-pi-a` and `rt-pi-b` with each Pi's address, `root`
   and its key.
   Also create `private/extra-local-nets.nix`, a Nix list of any networks on public
   addresses that tunnel traffic must not reach, or `[ ]`. Builds refuse to run without it.
2. Enable ARM builds on the machine that builds the images, then rebuild it:
   ```nix
   boot.binfmt.emulatedSystems = [ "aarch64-linux" ];
   ```
3. Build an image for each Pi, replacing `pi-a` with `pi-b` for the second, and flash
   it. Find the card with `lsblk` first, because `dd` wipes whatever it's given. `dd`
   can look stuck for a few minutes at the end while the data reaches the card, so
   wait for the prompt before removing it.
   `just build-image a` builds the image with the key from `.env`. Without just:
   ```sh
   nix build '.#nixosConfigurations.pi-a.config.system.build.sdImage' \
     --override-input admin-key path:/absolute/path/to/pi-a.pub \
     --override-input extra-local-nets path:$PWD/private/extra-local-nets.nix
   zstd -dc result/sd-image/*.img.zst | sudo dd of=/dev/sdX bs=4M conv=fsync status=progress
   ```
4. Connect the Pis:
   - **Pi A:** into a PoE port on apartment A's switch, with a fixed IP.
   - **Pi B:** its built-in port goes to the combo unit. TV B goes into the USB adapter.
5. Check each Pi, allowing a couple of minutes on first boot while its disk expands.
   `wan0` should have an address (`br0` on Pi B), the ping should get replies, `df`
   should show the whole card, and the last command should print nothing.
   ```sh
   ssh -i <key> root@<ip>
   ip -br addr show wan0     # br0 on Pi B
   ping -c 3 1.1.1.1
   df -h /
   journalctl -k | grep -i voltage
   ```
   On Pi B, `ip rule show priority 5260` prints a rule until the tunnel works, because
   TV B is on Pi B's own connection until then. Step 9 checks it's gone.
6. On the Headscale server, load `headscale/policy.json`, then create the user and
   keys. Keys expire after an hour unless you pass `--expiration`.
   ```sh
   headscale users create tunnel
   headscale preauthkeys create --tags tag:exit      # Pi A
   headscale preauthkeys create --tags tag:gateway   # Pi B
   headscale preauthkeys -u 1 create                 # phone
   ```
7. On each Pi, log in with its key:
   ```sh
   tailscale login --login-server=https://<headscale-host> --auth-key=<key>
   ```
8. On Pi A, re-apply its exit node setting, because logging in resets it. Pi B
   retries by itself.
   ```sh
   systemctl restart tailscaled-set
   ```
9. Check TV B is on the tunnel, because it also works without it, for example if Pi
   B's key expired before login. With the TV online and Pi B up for a few minutes,
   `ip rule show priority 5260` on Pi B should print nothing.

## Things to get right

- **Changing a Pi's config:** there's no need to re-flash. `just update-nix-on a`
  builds the new system on the workstation, copies it to the Pi over SSH and installs
  it for the Pi's next boot. `just reboot-pi a` then reboots into it. To switch to it
  at once instead, without just:
  ```sh
  NIX_SSHOPTS="-i <key>" nixos-rebuild switch --flake '.#pi-a' --target-host root@<ip> \
    --no-reexec --override-input admin-key path:/absolute/path/to/pi-a.pub \
    --override-input extra-local-nets path:$PWD/private/extra-local-nets.nix
  ```
  If a change moves the Pi's own address, `switch` loses the SSH connection halfway
  through. Use `boot` in its place, then reboot the Pi and find its new address.
- **SSH is the only way in:** root has no password, so a screen on HDMI only shows boot
  messages. If SSH doesn't work, fix the config and re-flash.
- **Reaching Pi B over SSH while the tunnel is up:** Pi B sends its own traffic through
  Pi A too, except to the network it's plugged into. So it only answers SSH from its
  own network, such as through Pi A with `ssh -J rt-pi-a rt-pi-b` while both are on
  apartment A's default VLAN, or over the tailnet. Its replies to anywhere else go
  into the tunnel, and Pi A drops those bound for private ranges. Once Pi B is in
  apartment B, the tailnet is the only way in from apartment A.
- **Heat and power:** the config sets up no fan control for the PoE HAT. With all four
  cores busy for 5 minutes, Pi A settled at about 65 °C with no undervoltage warnings.
  It hasn't been measured carrying tunnel traffic.
- **The clock:** a Pi has no battery-backed clock, so it can start with the wrong date
  until it reaches the network. Pi A's first boot logged 17 March, and a reboot had the
  right time within a minute. Log entries from early in a boot can show the wrong date.
- **Re-flashing a Pi:** delete its old node first (`headscale nodes list`, then
  `headscale nodes delete --identifier <id>`). Otherwise the new one joins as
  `pi-a-1`, while Pi B still looks for `pi-a`. After re-flashing Pi A, run
  `systemctl restart tailscaled-set` on Pi B as well. This hasn't been tested.
- **Checking which connection TV B is on:** the TV gives no sign when it moves. On
  Pi B, `ip rule show priority 5260` prints a rule while it's on apartment B's
  connection, and `journalctl -u tv-fallback` shows when it moved.
- **TV B's Wi-Fi:** make the TV forget the combo unit's Wi-Fi, or it can fall back
  to it and show apartment B's address.
- **Pi B's USB adapter:** connect only the TV. Pi B bridges it onto the apartment's
  network and sends everything from it through the tunnel, so a switch there would
  take other devices along, and a cable back to the apartment's network would loop
  it. `hosts/pi-b.nix` lists the supported adapter drivers.
- **Pi B's address:** it's on the bridge, `br0`, whose MAC address isn't the built-in
  port's. A fixed IP for Pi B in the router has to use `br0`'s (`ip link show br0`).
- **Casting to TV B:** phones find it the usual way, as long as they're on the same
  network as Pi B's built-in port. TV B can reach that network, and any network the
  router gives Pi B a route to over DHCP, but no other private network, the tailnet
  or Pi B itself.
- **Pi B's firewall:** the TV's port only comes up while Pi B's bridge rules are
  loaded, as without them TV B would bypass the tunnel. If nftables stops,
  `systemctl start nftables` brings both back.
- **Other networks:** the Pis' firewalls block the private ranges in
  `modules/local-nets.nix`, plus any ranges in `private/extra-local-nets.nix`. A network
  on public addresses has to be added there, or tunnel traffic could reach it.
- **The `private/` folder:** git ignores it. It holds the values specific to your site,
  and each Pi build reads `private/extra-local-nets.nix` from it.
