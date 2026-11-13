# The status endpoint the tunnel's dashboard reads over the tailnet:
# GET /status on TCP 9190, answered with JSON in schema 1. The contract, field by
# field, is in docs/decisions/D2-public-dashboard-pulls-from-the-pis.md.
#
# It's socket-activated with one service per connection, so nothing runs between
# requests. Each request runs one script, unprivileged, that reads /proc and
# /sys, asks tailscaled's local API over its socket, and builds the reply, with
# jq (tunnel.jq) for the part that comes from tailscaled's JSON. The processes
# one request starts:
# - both Pis: nc for tailscaled's status, stat for the root filesystem, and jq
#   unless tailscaled didn't answer;
# - a gateway, also: nc for one disco ping to its exit node, ip for rule 5260.
#
# nc rather than curl for the local API: it starts in a quarter of curl's CPU
# time, as curl links TLS and Kerberos libraries it never uses here, and on a
# Pi 3 that difference is a good part of a request's cost. It speaks HTTP/1.0,
# so tailscaled's answer is never chunked and ends when tailscaled closes the
# connection, and tunnel.jq checks its status line.
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
  #
  # bash builds the reply, checking every value it read against the shape its
  # field takes, and jq only the tunnel list from tailscaled's JSON: a jq
  # program for the whole reply took about twice the CPU.
  statusScript = pkgs.writeShellApplication {
    name = "pi-status";
    runtimeInputs = [ pkgs.coreutils pkgs.jq pkgs.netcat-openbsd ] ++ lib.optional gateway pkgs.iproute2;
    text = ''
      # systemd connects stdin and stdout to the client, and stderr to the
      # journal, where a line starting <4> is a warning.
      # extglob for the number checks below, which cost a tenth of a regex.
      shopt -s extglob nullglob

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

      # Set the variable named by $1 to the JSON for the text in $2, or to null
      # if the text isn't of that shape: a whole number, a decimal, or a 0 or 1
      # flag. No process.
      json_count() {
        if [[ $2 == @(0|[1-9]*([0-9])) ]]; then printf -v "''${1?}" '%s' "$2"; else printf -v "''${1?}" null; fi
      }
      json_decimal() {
        if [[ $2 == @(0|[1-9]*([0-9]))?(.+([0-9])) ]]; then printf -v "''${1?}" '%s' "$2"; else printf -v "''${1?}" null; fi
      }
      json_flag() {
        case $2 in
          1) printf -v "''${1?}" true ;;
          0) printf -v "''${1?}" false ;;
          *) printf -v "''${1?}" null ;;
        esac
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

      # tailscaled's local API, which lets any local user read the status, and
      # ping without asking for any permission at all. The here-string adds the
      # request's last \n.
      tailscale=$(nc -U -w 1 ${socket} 2> /dev/null \
        <<< $'GET /localapi/v0/status HTTP/1.0\r\nHost: local-tailscaled.sock\r\n\r') || true

      ping_ip="" ping="" gateway=null
      ${lib.optionalString gateway ''
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

        link=null speed=null operstate="" speed_text=""
        if [[ -e /sys/class/net/${cfg.tv} ]]; then
          first_line operstate /sys/class/net/${cfg.tv}/operstate
          case $operstate in
            up)
              link=true
              first_line speed_text /sys/class/net/${cfg.tv}/speed
              json_count speed "$speed_text"
              if [[ $speed == 0 ]]; then speed=null; fi
              ;;
            down | lowerlayerdown | dormant | notpresent) link=false ;;
          esac
        else
          # The adapter isn't plugged in.
          link=false
        fi

        # Only tv-port-gate writes it, and systemd removes it when the gate
        # stops, so without it the port isn't gated.
        gated=false verdict=""
        if [[ -e ${tvPortVerdict} ]]; then
          first_line verdict ${tvPortVerdict}
          case $verdict in
            loaded) gated=true ;;
            missing) gated=false ;;
            *) gated=null ;;
          esac
        fi

        # Reading rules over netlink takes no capability.
        fallback=null
        if rule=$(ip rule show priority ${toString fallbackPriority} 2> /dev/null); then
          if [[ -n $rule ]]; then fallback=true; else fallback=false; fi
        fi

        printf -v gateway '{"tv_port":{"link":%s,"speed_mbps":%s},"tv_port_gated":%s,"fallback":%s}' \
          "$link" "$speed" "$gated" "$fallback"
      ''}

      # TZ is UTC in the unit's environment.
      printf -v now '%(%Y-%m-%dT%H:%M:%SZ)T' -1

      uptime="" load1="" load5="" load15="" label=""
      user="" nice="" system="" idle="" iowait="" irq="" softirq="" steal=""
      { read -r uptime _; } 2> /dev/null < /proc/uptime || true
      json_count uptime "''${uptime%.*}"
      { read -r load1 load5 load15 _; } 2> /dev/null < /proc/loadavg || true
      json_decimal load1 "$load1"
      json_decimal load5 "$load5"
      json_decimal load15 "$load15"
      { read -r label user nice system idle iowait irq softirq steal _; } 2> /dev/null < /proc/stat || true
      if [[ $label != cpu ]]; then
        user="" nice="" system="" idle="" iowait="" irq="" softirq="" steal=""
      fi
      for tick in user nice system idle iowait irq softirq steal; do
        json_count "$tick" "''${!tick}"
      done

      mem_total="" mem_available=""
      while read -r key value _; do
        case $key in
          MemTotal:) mem_total=$value ;;
          MemAvailable:) mem_available=$value ;;
        esac
        if [[ -n $mem_total && -n $mem_available ]]; then break; fi
      done 2> /dev/null < /proc/meminfo || true
      json_count mem_total "$mem_total"
      json_count mem_available "$mem_available"

      # The SoC's, and the Pi firmware's undervoltage alarm, which the driver
      # polls every 2 seconds. A VM has neither. Millidegrees, to one decimal.
      type="" millidegrees="" temp=null
      for zone in /sys/class/thermal/thermal_zone*; do
        first_line type "$zone/type"
        if [[ $type == cpu-thermal ]]; then
          first_line millidegrees "$zone/temp"
          break
        fi
      done
      if [[ $millidegrees =~ ^(-?)([0-9]{1,9})$ ]]; then
        tenths=$(((10#''${BASH_REMATCH[2]} + 50) / 100))
        printf -v temp '%s%d.%d' "''${BASH_REMATCH[1]}" $((tenths / 10)) $((tenths % 10))
      fi
      name="" alarm="" undervoltage=null
      for hwmon in /sys/class/hwmon/hwmon*; do
        first_line name "$hwmon/name"
        if [[ $name == rpi_volt ]]; then
          first_line alarm "$hwmon/in0_lcrit_alarm"
          break
        fi
      done
      json_flag undervoltage "$alarm"

      # statvfs of the root filesystem: fragment size, blocks, free blocks and
      # blocks free to non-root.
      size="" blocks="" free="" avail=""
      read -r size blocks free avail < <(stat --file-system --format='%S %b %f %a' / 2> /dev/null) || true
      total_b=null used_b=null avail_b=null
      # Digit limits keep the products inside bash's 64-bit arithmetic.
      if [[ $size =~ ^[1-9][0-9]{0,5}$ && $blocks =~ ^[0-9]{1,12}$ ]]; then
        total_b=$((10#$size * 10#$blocks))
        if [[ $free =~ ^[0-9]{1,12}$ ]]; then used_b=$((10#$size * (10#$blocks - 10#$free))); fi
        if [[ $avail =~ ^[0-9]{1,12}$ ]]; then avail_b=$((10#$size * 10#$avail)); fi
      fi

      net="" rx="" tx=""
      for interface in ${lib.escapeShellArgs interfaces}; do
        first_line rx "/sys/class/net/$interface/statistics/rx_bytes"
        first_line tx "/sys/class/net/$interface/statistics/tx_bytes"
        json_count rx "$rx"
        json_count tx "$tx"
        net+="''${net:+,}\"$interface\":{\"rx_bytes\":$rx,\"tx_bytes\":$tx}"
      done

      # Written by the exit check as "<checked> <address>".
      checked="" address="" public_ip=null
      { read -r checked address _; } 2> /dev/null < ${publicIpCache} || true
      utc_time='^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$'
      ipv4='^((25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])\.){3}(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])$'
      if [[ $checked =~ $utc_time && $address =~ $ipv4 ]]; then
        printf -v public_ip '{"value":"%s","checked":"%s"}' "$address" "$checked"
      fi

      # Without an answer from tailscaled there's nothing for jq to read.
      tunnel=null
      if [[ -n $tailscale ]]; then
        if tunnel=$(jq --raw-input --slurp --compact-output --ascii-output \
          --arg role ${lib.escapeShellArg cfg.role} \
          --arg ping_ip "$ping_ip" \
          --arg ping "$ping" \
          --from-file ${./tunnel.jq} <<< "$tailscale"); then
          :
        else
          echo "<4>building the tunnel list failed: jq exited with $?" >&2
          reply "500 Internal Server Error" '{"schema":1,"error":"building the reply failed"}'
          exit 0
        fi
      fi

      printf -v body '{"schema":1,"host":%s,"role":%s,"time":"%s","uptime_s":%s,"cpu":{"load":[%s,%s,%s],"stat":{"user":%s,"nice":%s,"system":%s,"idle":%s,"iowait":%s,"irq":%s,"softirq":%s,"steal":%s}},"temp_c":%s,"undervoltage":%s,"undervoltage_events":null,"mem":{"total_kb":%s,"available_kb":%s},"disk":{"total_b":%s,"used_b":%s,"avail_b":%s},"net":{%s},"tunnel":%s,"public_ip":%s,"gateway":%s}' \
        ${lib.escapeShellArg (builtins.toJSON config.networking.hostName)} \
        ${lib.escapeShellArg (builtins.toJSON cfg.role)} \
        "$now" "$uptime" "$load1" "$load5" "$load15" \
        "$user" "$nice" "$system" "$idle" "$iowait" "$irq" "$softirq" "$steal" \
        "$temp" "$undervoltage" "$mem_total" "$mem_available" \
        "$total_b" "$used_b" "$avail_b" "$net" "$tunnel" "$public_ip" "$gateway"
      reply "200 OK" "$body"
    '';
  };

  # The exit check: which public address this Pi's own traffic leaves from,
  # through its normal routing, so Pi B's goes through Pi A while the tunnel
  # works. The dashboard compares the Pis' answers. It runs from its timer,
  # never while a request waits, and caches "<checked> <address>" for the
  # endpoint. A failed check leaves the last result as it was, and the address
  # never goes to the journal.
  exitCheck = pkgs.writeShellApplication {
    name = "pi-exit-check";
    runtimeInputs = [ pkgs.coreutils pkgs.curlMinimal ];
    text = ''
      # IPv4 only: Pi A forwards only IPv4, so it's the one address the Pis can
      # compare.
      status=0
      answer=$(curl --ipv4 --silent --fail --max-time 10 --max-filesize 64 --proto =http,https \
        ${lib.escapeShellArg cfg.publicIpUrl}) || status=$?
      if ((status != 0)); then
        echo "<4>the exit check's site didn't answer (curl exited with $status), so the last result stays" >&2
        exit 0
      fi
      address=''${answer//[$' \t\r\n']/}
      ipv4='^((25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])\.){3}(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])$'
      if [[ ! $address =~ $ipv4 ]]; then
        echo "<4>the exit check's site didn't answer with an IPv4 address, so the last result stays" >&2
        exit 0
      fi
      # TZ is UTC in the unit's environment.
      printf -v checked '%(%Y-%m-%dT%H:%M:%SZ)T' -1
      printf '%s %s\n' "$checked" "$address" > ${publicIpCache}.new
      mv -f ${publicIpCache}.new ${publicIpCache}
    '';
  };
in
{
  options.tunnel = {
    role = lib.mkOption {
      type = lib.types.enum [ "exit" "gateway" ];
      description = "What this Pi does in the tunnel: the exit node, or a gateway that sends its TV's traffic through the exit node.";
    };
    publicIpUrl = lib.mkOption {
      type = lib.types.str;
      default = "https://ipv4.icanhazip.com/";
      description = "Page that answers with the IPv4 address it sees, as plain text. The exit check fetches it every 5 minutes through the Pi's normal routing, so the site sees each Pi's public address.";
    };
  };

  config = {
    # The script writes the interface names into the reply as they are.
    assertions = [
      {
        assertion = lib.all (name: builtins.match "[A-Za-z0-9_.-]+" name != null) interfaces;
        message = "The status endpoint reports interfaces by name, which must be letters, digits, '_', '.' or '-': ${toString interfaces}";
      }
    ];

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

    systemd.timers.pi-exit-check = {
      description = "Check this Pi's public address every 5 minutes";
      wantedBy = [ "timers.target" ];
      timerConfig = {
        OnBootSec = "1min";
        OnUnitActiveSec = "5min";
        AccuracySec = "1s";
      };
    };

    systemd.services.pi-exit-check = {
      description = "Check which public address this Pi's traffic leaves from";
      environment = {
        LC_ALL = "C";
        TZ = "UTC0";
      };
      serviceConfig = {
        Type = "oneshot";
        ExecStart = lib.getExe exitCheck;
        # Its own deadline, beyond curl's 10 seconds and well inside the timer's 5
        # minutes.
        TimeoutStartSec = "30s";
        # A user of its own rather than a DynamicUser: with one, systemd keeps a
        # preserved runtime directory under /run/private, where the status
        # endpoint's user couldn't read it. Kept between runs for the endpoint,
        # and gone at boot, so the endpoint reports null until the first check.
        User = "pi-exit-check";
        Group = "pi-exit-check";
        RuntimeDirectory = "pi-exit-check";
        RuntimeDirectoryPreserve = true;
        RuntimeDirectoryMode = "0755";
        UMask = "0022";
        Nice = 10;

        # It only needs to reach the site: IP, and AF_UNIX for name lookups
        # through nscd. No capabilities, and nothing writable but its directory.
        CapabilityBoundingSet = "";
        NoNewPrivileges = true;
        RestrictAddressFamilies = [ "AF_UNIX" "AF_INET" "AF_INET6" ];
        PrivateDevices = true;
        PrivateTmp = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        ProtectClock = true;
        ProtectControlGroups = true;
        ProtectHostname = true;
        ProtectKernelLogs = true;
        ProtectKernelModules = true;
        ProtectKernelTunables = true;
        ProtectProc = "invisible";
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        SystemCallArchitectures = "native";
        SystemCallFilter = [ "@system-service" "~@privileged" ];
      };
    };

    users.users.pi-exit-check = {
      isSystemUser = true;
      group = "pi-exit-check";
      description = "Exit check for the tunnel's status endpoint";
    };
    users.groups.pi-exit-check = { };
  };
}
