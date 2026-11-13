# Apartment B: TV B plugs in here. Pi B bridges it onto this apartment's
# network, so phones here can find it and cast to it, but sends its internet
# traffic through Pi A. If the tunnel stops working the TV moves to this
# apartment's connection until it's back, so it stays online but shows this
# apartment's IP in the meantime.
{ config, lib, pkgs, ... }:

let
  cfg = config.tunnel;
  localNetList = import ../modules/local-nets.nix ++ cfg.extraLocalNets;
  localNets = lib.concatStringsSep ", " localNetList;
  # tailscale0's MTU is 1280, minus 40 bytes of IPv4 and TCP headers.
  tunnelMss = "1240";
  # Tailscale looks up its exit node route at 5270. These sit either side of it.
  lanPriority = 5255;
  privatePriority = 5257;
  fallbackPriority = 5260;
  holdPriority = 5280;
  # Marks the TV's connections. Clear of the bits Tailscale uses, 0xff0000.
  tvMark = "0x1";

  # Checks every couple of seconds rather than following the adapter's device unit,
  # which misses an adapter that comes back while this is still stopping, as after
  # a quick reset. That also takes the port down if the table goes missing, or is
  # flushed, while nftables stays active.
  #
  # Each verdict goes to tvPortVerdict as "loaded" or "missing", for the status
  # endpoint in modules/status.nix, which mustn't hold the CAP_NET_ADMIN that
  # reading nftables takes. It's only written when it changes, through a rename so
  # a reader never sees half of it. A failed write can't stop the gate: it removes
  # the old verdict rather than leave it standing, and the next round tries again.
  # systemd removes the whole directory when the gate stops.
  tvPortVerdict = "/run/tv-port-gate/verdict";
  tvPortGate = pkgs.writeShellApplication {
    name = "tv-port-gate";
    runtimeInputs = [ pkgs.coreutils pkgs.iproute2 pkgs.nftables ];
    text = ''
      reported=
      while true; do
        # The broute rule itself, so a flushed table counts as missing.
        rules=$(nft list chain bridge tv-port prerouting 2> /dev/null || true)
        if [[ $rules == *"meta broute set 1"* ]]; then
          ip link set ${cfg.tv} up 2> /dev/null || true
          verdict=loaded
        else
          ip link set ${cfg.tv} down 2> /dev/null || true
          verdict=missing
        fi
        if [[ $verdict != "$reported" ]]; then
          if echo "$verdict" > ${tvPortVerdict}.new && mv -f ${tvPortVerdict}.new ${tvPortVerdict}; then
            reported=$verdict
          else
            rm -f ${tvPortVerdict} || true
          fi
        fi
        sleep 2
      done
    '';
  };

  tvFallback = pkgs.writeShellApplication {
    name = "tv-fallback";
    runtimeInputs = [ pkgs.conntrack-tools pkgs.iproute2 pkgs.iputils ];
    text = ''
      targets=(${lib.escapeShellArgs cfg.probeTargets})

      answers_through() {
        for target in "''${targets[@]}"; do
          ping -c 1 -W 2 -I "$1" "$target" > /dev/null 2>&1 && return 0
        done
        return 1
      }

      on_fallback() {
        [ -n "$(ip rule show priority ${toString fallbackPriority})" ]
      }

      # Connections made over the other path can't carry on. Dropping them lets the
      # TV reconnect instead of stalling. conntrack exits non-zero if there are none.
      reset_tv_connections() {
        conntrack -D --mark ${tvMark}/${tvMark} > /dev/null 2>&1 || true
      }

      # Lets the tunnel come up after boot, so the TV doesn't start on this
      # apartment's connection.
      sleep 40

      misses=0
      hits=0
      while true; do
        # Out to the internet rather than just to Pi A, so this also notices Pi A being
        # up with nothing working behind it.
        if answers_through tailscale0; then
          hits=$((hits + 1))
          misses=0
        elif answers_through ${cfg.wan}; then
          misses=$((misses + 1))
          hits=0
        else
          # This apartment's connection is down too, which takes the tunnel with it.
          # Moving wouldn't help, and when the connection returned the TV would be on
          # it ahead of the tunnel.
          misses=0
          hits=0
        fi

        # About 20 seconds to move away and 30 to move back, so a short blip doesn't
        # reset the TV's connections.
        if on_fallback; then
          if [ "$hits" -ge 10 ]; then
            ip rule del priority ${toString fallbackPriority}
            reset_tv_connections
            echo "tunnel is back, TV moved to it"
          fi
        elif [ "$misses" -ge 3 ]; then
          ip rule add iif ${cfg.tv} lookup main priority ${toString fallbackPriority}
          reset_tv_connections
          echo "tunnel is down, TV moved to this apartment's connection"
        fi

        sleep 3
      done
    '';
  };
in
{
  networking.hostName = "pi-b";

  # Pi B's own address is on the bridge.
  tunnel.wan = "br0";

  services.tailscale.extraSetFlags = [
    "--exit-node=pi-a"
    # Without this, replies to the TV would be routed back into the tunnel.
    "--exit-node-allow-lan-access=true"
  ];

  # USB Ethernet adapters; matched by driver as the port numbering can change.
  systemd.network.links."11-tv0" = {
    matchConfig.Driver = "r8152 asix ax88179_178a cdc_ether cdc_ncm";
    linkConfig.Name = "tv0";
  };

  # The TV's port joins this apartment's network, so the TV gets its address from
  # the apartment's router like any other device, and phones find it the usual
  # way, by multicast. A router in between would stop that multicast.
  systemd.network.netdevs."10-bridge" = {
    netdevConfig = {
      Kind = "bridge";
      Name = cfg.wan;
    };
    bridgeConfig = {
      STP = false;
      # With two ports and no IGMP querier on a home network, snooping can only
      # lose multicast the phones and the TV need.
      MulticastSnooping = false;
    };
  };

  systemd.network.networks."11-uplink" = {
    matchConfig.Name = cfg.uplink;
    networkConfig.Bridge = cfg.wan;
    linkConfig.RequiredForOnline = "enslaved";
  };

  systemd.network.networks."11-tv" = {
    matchConfig.Name = cfg.tv;
    networkConfig = {
      Bridge = cfg.wan;
      LinkLocalAddressing = "no";
      IPv6AcceptRA = false;
      # The TV's packets reach routing on this port, which has no IPv4 address,
      # and the kernel's reverse path filter drops anything arriving on such a
      # port, even in loose mode. NixOS's firewall still checks reverse paths.
      IPv4ReversePathFilter = "no";
    };
    linkConfig = {
      RequiredForOnline = "no";
      # Left to tv-port-gate below.
      ActivationPolicy = "manual";
    };
  };

  # The TV's port only comes up while the tv-port table is loaded. Without it the
  # TV's traffic would cross the bridge straight to this apartment's router,
  # skipping the tunnel and the hold. That covers a failed load at boot, nftables
  # being stopped, and the end of a shutdown: ordered after nftables, this stops
  # first and takes the port down before nftables removes its tables.
  systemd.services.tv-port-gate = {
    description = "Bring up TV B's port while the firewall that keeps it on the tunnel is loaded";
    wantedBy = [ "multi-user.target" "nftables.service" ];
    bindsTo = [ "nftables.service" ];
    partOf = [ "nftables.service" ];
    after = [ "nftables.service" ];
    serviceConfig = {
      ExecStart = lib.getExe tvPortGate;
      # Also after a crash. The adapter may already be gone.
      ExecStopPost = "-${pkgs.iproute2}/bin/ip link set ${cfg.tv} down";
      Restart = "always";
      RestartSec = "2s";
      # Holds the verdict, and goes away whenever the gate stops, restarts included.
      RuntimeDirectory = "tv-port-gate";
    };
    unitConfig.StartLimitIntervalSec = 0;
  };

  # On the bridge rather than the TV's port, so they stay in place while the TV's
  # adapter is unplugged.
  systemd.network.networks."10-wan".routingPolicyRules = [
    # The TV's own network, where the phones are, never goes through the tunnel,
    # and stays reachable while the tunnel is down.
    {
      IncomingInterface = cfg.tv;
      Table = "main";
      SuppressPrefixLength = 0;
      Priority = lanPriority;
    }
  ]
  # Every other private network, whichever way the TV's traffic would go. On
  # fallback this apartment's router may reach some, such as apartment A's secure
  # VLAN while Pi B is on trial there.
  ++ map (net: {
    IncomingInterface = cfg.tv;
    To = net;
    Type = "blackhole";
    Priority = privatePriority;
  }) localNetList
  ++ [
    # Without Tailscale's route, the TV's traffic would carry on to the main table
    # and out through this apartment's connection, before tv-fallback has decided.
    {
      IncomingInterface = cfg.tv;
      Type = "blackhole";
      Priority = holdPriority;
    }
  ];

  systemd.services.tv-fallback = {
    description = "Move TV B to this apartment's connection while the tunnel is down";
    wantedBy = [ "multi-user.target" ];
    serviceConfig = {
      ExecStart = lib.getExe tvFallback;
      Restart = "always";
      RestartSec = "5s";
    };
    unitConfig.StartLimitIntervalSec = 0;
  };

  # Broadcast and multicast from the TV cross the bridge, which is how phones and
  # the TV find each other, and so does ARP. Every IPv4 packet the TV sends to a
  # single address goes up to Pi B's routing instead, as if the TV were behind a
  # router, and arrives on the TV's port. Routing then picks this network, the
  # tunnel or this apartment's connection. ARP has to stay on the bridge: the TV's
  # replies would otherwise arrive on the TV's port, where nothing asked, and be
  # dropped. The port carries only IPv4 and ARP, as IPv6 would give the TV a path
  # that skips the tunnel.
  networking.nftables.tables.tv-port = {
    family = "bridge";
    content = ''
      chain prerouting {
        type filter hook prerouting priority filter; policy accept;
        iifname "${cfg.tv}" ether type != { ip, arp } drop
        iifname "${cfg.tv}" ether type ip meta pkttype != { broadcast, multicast } meta broute set 1 meta pkttype set host
      }

      chain forward {
        type filter hook forward priority filter; policy accept;
        oifname "${cfg.tv}" ether type != { ip, arp } drop
      }

      chain output {
        type filter hook output priority filter; policy accept;
        oifname "${cfg.tv}" ether type != { ip, arp } drop
      }
    '';
  };

  networking.nftables.tables.tv-gateway = {
    family = "inet";
    content = ''
      chain prerouting {
        type nat hook prerouting priority dstnat; policy accept;
        iifname "${cfg.tv}" ct mark set ct mark or ${tvMark}
        # Public resolvers whichever server the TV asks, so DNS also leaves through Pi A.
        iifname "${cfg.tv}" meta l4proto { tcp, udp } th dport 53 dnat ip to ${cfg.tvDns}
      }

      chain forward {
        type filter hook forward priority filter; policy drop;
        iifname "tailscale0" tcp flags & syn == syn tcp option maxseg size > ${tunnelMss} tcp option maxseg size set ${tunnelMss}
        oifname "tailscale0" tcp flags & syn == syn tcp option maxseg size > ${tunnelMss} tcp option maxseg size set ${tunnelMss}
        ct state established,related accept
        # The TV's own network, and this apartment's connection while on fallback.
        # When a phone connects to the TV, Pi B only sees the TV's half, as the
        # phone's crosses the bridge, so this can't wait for an established state.
        iifname "${cfg.tv}" oifname "${cfg.wan}" meta nfproto ipv4 accept
        iifname "${cfg.tv}" oifname "tailscale0" ip daddr { ${localNets} } drop
        iifname "${cfg.tv}" oifname "tailscale0" meta nfproto ipv4 accept
      }

      chain postrouting {
        type nat hook postrouting priority srcnat; policy accept;
        oifname "tailscale0" ct mark and ${tvMark} == ${tvMark} masquerade
        # The redirected DNS on this apartment's connection. Its replies would
        # otherwise come from the router straight across the bridge, from the
        # resolver's address rather than the one the TV asked, and the TV would
        # drop them. This brings them back through Pi B to be translated.
        oifname "${cfg.wan}" ct status dnat ct mark and ${tvMark} == ${tvMark} masquerade
      }
    '';
  };
}
