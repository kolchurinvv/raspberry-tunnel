# The status endpoint the tunnel's dashboard reads over the tailnet:
# GET /status on TCP 9190, answered with JSON in schema 1. The contract, field by
# field, is in docs/decisions/D2-public-dashboard-pulls-from-the-pis.md.
#
# It's socket-activated with one service per connection, so nothing runs between
# requests. Each request runs one script, unprivileged, that reads /proc and
# /sys, asks tailscaled's local API over its socket, and hands what it read to
# jq (status.jq), which builds the reply. The processes one request starts:
# - both Pis: nc for tailscaled's status, stat for the root filesystem, jq;
# - a gateway, also: nc for one disco ping to its exit node, ip for rule 5260.
#
# nc rather than curl for the local API: it starts in a quarter of curl's CPU
# time, as curl links TLS and Kerberos libraries it never uses here, and on a
# Pi 3 that difference is a good part of a request's cost. It speaks HTTP/1.0,
# so tailscaled's answer is never chunked and ends when tailscaled closes the
# connection, and status.jq checks its status line.
{ config, lib, pkgs, ... }:

let
  cfg = config.tunnel;
  # Fixed by D2's contract.
  port = 9190;
  gateway = cfg.role == "gateway";
  socket = "/run/tailscale/tailscaled.sock";

  # The interfaces whose byte counters the reply carries: wan0, br0, tv0 and
  # tailscale0 on the Pis, where each one exists.
  interfaces =
    if gateway then
      [ cfg.uplink cfg.wan cfg.tv "tailscale0" ]
    else
      [ cfg.wan "tailscale0" ];

  # Written by the exit check below, and by tv-port-gate in hosts/pi-b.nix.
  publicIpCache = "/run/pi-exit-check/public-ip";
  tvPortVerdict = "/run/tv-port-gate/verdict";
  # tv-fallback's rule in hosts/pi-b.nix, set while TV B is on apartment B's
  # connection.
  fallbackPriority = 5260;

  # Every step that waits has a deadline, so the reply arrives within D2's 2
  # seconds: nc gives up on tailscaled after a second without data, and the
  # rest is local. Only the ping normally takes that second, when no pong comes.
  statusScript = pkgs.writeShellApplication {
    name = "pi-status";
    runtimeInputs = [ pkgs.coreutils pkgs.jq pkgs.netcat-openbsd ] ++ lib.optional gateway pkgs.iproute2;
    text = ''
      # systemd connects stdin and stdout to the client, and stderr to the
      # journal, where a line starting <4> is a warning.
      shopt -s nullglob

      replied=
      reply() {
        local status=$1 body=$2
        printf 'HTTP/1.1 %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\nCache-Control: no-store\r\nConnection: close\r\n\r\n%s' \
          "$status" "''${#body}" "$body"
        replied=1
      }
      # Whatever stops the script before it replies, the client still gets a 500.
      trap '[[ -n $replied ]] || reply "500 Internal Server Error" "{\"schema\":1,\"error\":\"the status script stopped before replying\"}"' EXIT

      # Reads a file's first line into the variable named by $1, which stays
      # empty if the file is missing or can't be read. No process.
      first_line() {
        printf -v "''${1?}" '%s' ""
        { IFS= read -r "''${1?}"; } 2> /dev/null < "$2" || true
      }

      # The request line, then the headers up to the blank line. All of them:
      # closing a connection with unread data sends a reset, which can cost the
      # client the reply.
      request=
      IFS= read -r -t 1 -n 4096 request || true
      request=''${request%$'\r'}
      for ((i = 0; i < 64; i++)); do
        IFS= read -r -t 1 -n 4096 header || break
        [[ -n ''${header%$'\r'} ]] || break
      done
      request_line='^([A-Z]+) ([^ ]+) HTTP/1\.[01]$'
      if [[ ! $request =~ $request_line ]]; then
        reply "400 Bad Request" '{"schema":1,"error":"bad request"}'
        exit 0
      fi
      method=''${BASH_REMATCH[1]}
      path=''${BASH_REMATCH[2]%%\?*}
      if [[ $path != /status ]]; then
        reply "404 Not Found" '{"schema":1,"error":"not found"}'
        exit 0
      fi
      if [[ $method != GET ]]; then
        reply "405 Method Not Allowed" '{"schema":1,"error":"only GET"}'
        exit 0
      fi

      uptime="" loadavg="" cpu="" type="" name="" rx="" tx="" public_ip=""

      # TZ is UTC in the unit's environment.
      printf -v now '%(%Y-%m-%dT%H:%M:%SZ)T' -1
      first_line uptime /proc/uptime
      first_line loadavg /proc/loadavg
      first_line cpu /proc/stat

      mem_total="" mem_available=""
      while read -r key value _; do
        case $key in
          MemTotal:) mem_total=$value ;;
          MemAvailable:) mem_available=$value ;;
        esac
        if [[ -n $mem_total && -n $mem_available ]]; then break; fi
      done 2> /dev/null < /proc/meminfo || true

      # The SoC's, and the Pi firmware's undervoltage alarm, which the driver
      # polls every 2 seconds. A VM has neither.
      temp=
      for zone in /sys/class/thermal/thermal_zone*; do
        first_line type "$zone/type"
        if [[ $type == cpu-thermal ]]; then
          first_line temp "$zone/temp"
          break
        fi
      done
      undervoltage=
      for hwmon in /sys/class/hwmon/hwmon*; do
        first_line name "$hwmon/name"
        if [[ $name == rpi_volt ]]; then
          first_line undervoltage "$hwmon/in0_lcrit_alarm"
          break
        fi
      done

      disk=$(stat --file-system --format='%S %b %f %a' / 2> /dev/null) || true

      net=""
      for interface in ${lib.escapeShellArgs interfaces}; do
        first_line rx "/sys/class/net/$interface/statistics/rx_bytes"
        first_line tx "/sys/class/net/$interface/statistics/tx_bytes"
        net+="$interface ''${rx:--} ''${tx:--}"$'\n'
      done

      # tailscaled's local API, which lets any local user read the status, and
      # ping without asking for any permission at all. The here-string adds the
      # request's last \n.
      tailscale=$(nc -U -w 1 ${socket} 2> /dev/null \
        <<< $'GET /localapi/v0/status HTTP/1.0\r\nHost: local-tailscaled.sock\r\n\r') || true

      ping_ip="" ping="" tv_link="" tv_speed="" tv_port_gated="" fallback=""
      ${lib.optionalString gateway ''
        operstate="" verdict=""

        # The exit node's IPv4 address, to ping it. Only a pointer: jq checks
        # the answer came from the address the full status gives the exit node.
        exit_node_ip='"ExitNodeStatus":[[:space:]]*\{[^}]*"TailscaleIPs":[[:space:]]*\[[^]]*"(([0-9]{1,3}\.){3}[0-9]{1,3})/32"'
        if [[ $tailscale =~ $exit_node_ip ]]; then
          ping_ip=''${BASH_REMATCH[1]}
          # One attempt. Without a pong tailscaled never answers, so nc's
          # deadline is all that ends it.
          ping=$(nc -U -w 1 ${socket} 2> /dev/null \
            <<< "POST /localapi/v0/ping?ip=$ping_ip&type=disco HTTP/1.0"$'\r\nHost: local-tailscaled.sock\r\nContent-Length: 0\r\n\r') || true
        fi

        if [[ -e /sys/class/net/${cfg.tv} ]]; then
          first_line operstate /sys/class/net/${cfg.tv}/operstate
          case $operstate in
            up)
              tv_link=1
              first_line tv_speed /sys/class/net/${cfg.tv}/speed
              ;;
            down | lowerlayerdown | dormant | notpresent) tv_link=0 ;;
          esac
        else
          # The adapter isn't plugged in.
          tv_link=0
        fi

        # Only tv-port-gate writes it, and systemd removes it when the gate
        # stops, so without it the port isn't gated.
        if [[ -e ${tvPortVerdict} ]]; then
          first_line verdict ${tvPortVerdict}
          case $verdict in
            loaded) tv_port_gated=1 ;;
            missing) tv_port_gated=0 ;;
          esac
        else
          tv_port_gated=0
        fi

        # Reading rules over netlink takes no capability.
        if rule=$(ip rule show priority ${toString fallbackPriority} 2> /dev/null); then
          if [[ -n $rule ]]; then fallback=1; else fallback=0; fi
        fi
      ''}

      first_line public_ip ${publicIpCache}

      if body=$(jq --raw-input --slurp --compact-output --ascii-output \
        --arg host ${lib.escapeShellArg config.networking.hostName} \
        --arg role ${lib.escapeShellArg cfg.role} \
        --arg time "$now" \
        --arg uptime "$uptime" \
        --arg loadavg "$loadavg" \
        --arg cpu "$cpu" \
        --arg temp "$temp" \
        --arg undervoltage "$undervoltage" \
        --arg mem_total "$mem_total" \
        --arg mem_available "$mem_available" \
        --arg disk "$disk" \
        --arg net "$net" \
        --arg ping_ip "$ping_ip" \
        --arg ping "$ping" \
        --arg public_ip "$public_ip" \
        --arg tv_link "$tv_link" \
        --arg tv_speed "$tv_speed" \
        --arg tv_port_gated "$tv_port_gated" \
        --arg fallback "$fallback" \
        --from-file ${./status.jq} <<< "$tailscale"); then
        reply "200 OK" "$body"
      else
        echo "<4>building the status reply failed: jq exited with $?" >&2
        reply "500 Internal Server Error" '{"schema":1,"error":"building the reply failed"}'
      fi
    '';
  };
in
{
  options.tunnel.role = lib.mkOption {
    type = lib.types.enum [ "exit" "gateway" ];
    description = "What this Pi does in the tunnel: the exit node, or a gateway that sends its TV's traffic through the exit node.";
  };

  config = {
    systemd.sockets.pi-status = {
      description = "Status endpoint for the tunnel's dashboard";
      wantedBy = [ "sockets.target" ];
      listenStreams = [ (toString port) ];
      socketConfig = {
        Accept = true;
        # The dashboard asks every 30 seconds. More at once would only load the Pi.
        MaxConnections = 4;
      };
    };

    systemd.services."pi-status@" = {
      description = "Answer one request to the tunnel's status endpoint";
      # C skips loading the locale archive in every process the request starts.
      environment = {
        LC_ALL = "C";
        TZ = "UTC0";
      };
      serviceConfig = {
        ExecStart = lib.getExe statusScript;
        StandardInput = "socket";
        StandardOutput = "socket";
        StandardError = "journal";
        # Ends a stuck request well after its reply was due.
        RuntimeMaxSec = 3;
        # A failed request doesn't stay behind in systemctl --failed.
        CollectMode = "inactive-or-failed";
        # systemd's info lines name each connection's addresses, twice a minute.
        # Warnings and errors still show.
        LogLevelMax = "notice";
        # Behind tailscaled and the TV's traffic.
        Nice = 10;
        MemoryMax = "64M";
        TasksMax = 16;

        # Nothing it reads needs a privilege: /proc, /sys, rules over netlink,
        # the files under /run that the exit check and tv-port-gate write, and
        # tailscaled's socket, which is world-writable and gives any local user
        # read access to the local API. So it runs as a throwaway user with no
        # capabilities, and no address family beyond what it uses: AF_UNIX for
        # tailscaled, and on a gateway AF_NETLINK for ip rule.
        DynamicUser = true;
        User = "pi-status";
        CapabilityBoundingSet = "";
        NoNewPrivileges = true;
        RestrictAddressFamilies = [ "AF_UNIX" ] ++ lib.optional gateway "AF_NETLINK";
        PrivateDevices = true;
        ProtectClock = true;
        ProtectControlGroups = true;
        ProtectHome = true;
        ProtectHostname = true;
        ProtectKernelLogs = true;
        ProtectKernelModules = true;
        ProtectKernelTunables = true;
        ProtectProc = "invisible";
        RestrictNamespaces = true;
        RestrictRealtime = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        SystemCallArchitectures = "native";
        SystemCallFilter = [ "@system-service" "~@privileged" ];
        UMask = "0077";
      };
    };

    # Like SSH in modules/common.nix: the tailnet only, never the TV's port or
    # either apartment's network.
    networking.firewall.interfaces.tailscale0.allowedTCPPorts = [ port ];
  };
}
