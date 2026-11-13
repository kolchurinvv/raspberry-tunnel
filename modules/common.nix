{ config, lib, ... }:

let
  cfg = config.tunnel;
in
{
  imports = [ ./status.nix ];

  options.tunnel = {
    wan = lib.mkOption {
      type = lib.types.str;
      default = "wan0";
      description = "Interface facing the apartment's router. On Pi B, the bridge holding the uplink and the TV's port.";
    };
    uplink = lib.mkOption {
      type = lib.types.str;
      default = "wan0";
      description = "Port cabled to the apartment's router, which Pi B bridges with the TV's port. Only used on Pi B.";
    };
    tv = lib.mkOption {
      type = lib.types.str;
      default = "tv0";
      description = "Interface facing the TV. Only used on Pi B.";
    };
    tvDns = lib.mkOption {
      type = lib.types.str;
      default = "1.1.1.1";
      description = "Resolver the TV's DNS queries go to, whichever server it asks, so they also leave through the tunnel. Only used on Pi B.";
    };
    # No default on purpose: forgetting it must stop the build, not open the firewall.
    extraLocalNets = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      description = "Ranges tunnel traffic may never reach, on top of modules/local-nets.nix. Use it for networks on public address space, which the private ranges don't cover.";
    };
    probeTargets = lib.mkOption {
      type = lib.types.nonEmptyListOf lib.types.str;
      default = [ "1.1.1.1" "8.8.8.8" ];
      description = "Internet addresses pinged through the tunnel to see if it works. Only used on Pi B.";
    };
  };

  config = {
    networking.useNetworkd = true;
    networking.useDHCP = false;
    networking.nftables.enable = true;

    systemd.network.networks."10-wan" = {
      matchConfig.Name = cfg.wan;
      networkConfig.DHCP = "yes";
    };
    # Pi B's TV port is often unplugged; that must not hold up boot.
    systemd.network.wait-online.anyInterface = true;
    # By default networkd deletes routes and rules it didn't create whenever it
    # restarts or sees a new link. That includes Tailscale's, and Tailscale doesn't
    # put all of them back, which cuts Pi B off from its own LAN.
    systemd.network.config.networkConfig = {
      ManageForeignRoutes = false;
      ManageForeignRoutingPolicyRules = false;
    };

    services.tailscale = {
      enable = true;
      openFirewall = true;
      useRoutingFeatures = "both";
      disableUpstreamLogging = true;
      # The Pis keep using their router's DNS, so they don't depend on the tailnet's.
      extraSetFlags = [ "--accept-dns=false" ];
    };
    # Auto-detection can pick iptables, whose rules wouldn't sit alongside ours.
    systemd.services.tailscaled.serviceConfig.Environment = [ "TS_DEBUG_FIREWALL_MODE=nftables" ];
    # Fails until Tailscale is up and Pi A is visible, which is normal after boot.
    systemd.services.tailscaled-set = {
      serviceConfig = {
        Restart = "on-failure";
        RestartSec = "10s";
      };
      unitConfig.StartLimitIntervalSec = 0;
    };

    services.openssh = {
      enable = true;
      # Opened per interface below, so it stays closed to the TV.
      openFirewall = false;
      settings = {
        PasswordAuthentication = false;
        KbdInteractiveAuthentication = false;
        PermitRootLogin = "prohibit-password";
      };
    };
    networking.firewall.interfaces.${cfg.wan}.allowedTCPPorts = [ 22 ];
    networking.firewall.interfaces.tailscale0.allowedTCPPorts = [ 22 ];

    time.timeZone = "Europe/Prague";
    system.stateVersion = "26.05";
  };
}
