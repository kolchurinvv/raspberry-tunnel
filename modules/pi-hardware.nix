# adminKey is the flake's admin-key input, the Pi's SSH public key.
# extraLocalNetsFile is the flake's extra-local-nets input, a Nix list of ranges.
{ lib, modulesPath, adminKey, extraLocalNetsFile, ... }:

let
  extraLocalNets = import extraLocalNetsFile;
in
{
  imports = [ "${modulesPath}/installer/sd-card/sd-image-aarch64.nix" ];

  nixpkgs.hostPlatform = "aarch64-linux";

  # The SD image profile enables it by default, and nothing here needs it.
  boot.supportedFilesystems.zfs = lib.mkForce false;

  assertions = [
    {
      assertion = lib.hasPrefix "ssh-" (builtins.readFile adminKey);
      # Every build sets root's accepted keys, not just the image, so a missing or
      # wrong key locks you out at the Pi's next activation.
      message = "Give this Pi's SSH public key with --override-input admin-key path:/absolute/path/to/key.pub, or the Pi will lock you out.";
    }
  ];
  users.users.root.openssh.authorizedKeys.keyFiles = [ adminKey ];

  # Without its real value, the firewall would let tunnel traffic reach networks
  # it's meant to block, so a build without one stops here.
  tunnel.extraLocalNets =
    if extraLocalNets == null then
      throw "Give this build its ranges with --override-input extra-local-nets path:/absolute/path/to/extra-local-nets.nix, a Nix list, [ ] if there are none."
    else
      extraLocalNets;

  # USB port numbering decides the default names, so match on the driver instead.
  systemd.network.links."10-wan0" = {
    matchConfig.Driver = "lan78xx";
    linkConfig.Name = "wan0";
  };

  # Logs stay in RAM to spare the SD card.
  services.journald.settings.Journal = {
    Storage = "volatile";
    RuntimeMaxUse = "32M";
  };
  zramSwap.enable = true;
  programs.tmux.enable = true;

  nix.gc = {
    automatic = true;
    dates = "weekly";
    options = "--delete-older-than 14d";
  };
}
