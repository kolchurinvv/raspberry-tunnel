# Fiber apartment: the exit node. TV B's traffic reaches the internet from here.
{ config, lib, ... }:

let
  localNets = lib.concatStringsSep ", " (import ../modules/local-nets.nix ++ config.tunnel.extraLocalNets);
in
{
  networking.hostName = "pi-a";

  services.tailscale.extraSetFlags = [ "--advertise-exit-node" ];

  networking.nftables.tables.tunnel-exit = {
    family = "inet";
    content = ''
      chain forward {
        type filter hook forward priority filter; policy drop;
        ct state established,related accept
        iifname "tailscale0" oifname "${config.tunnel.wan}" ip daddr { ${localNets} } drop
        iifname "tailscale0" oifname "${config.tunnel.wan}" meta nfproto ipv4 accept
      }
    '';
  };
}
