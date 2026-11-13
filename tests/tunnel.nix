# Runs the real Pi A and Pi B roles in VMs against a real Headscale, and checks
# where TV B's traffic goes. VLAN 1 stands in for the internet plus apartment A's
# LAN, VLAN 2 for apartment B's network behind its router, and VLAN 3 for the
# cable between Pi B and the TV. The site on the internet is also the dashboard,
# which reads both Pis' status endpoint over the tailnet (D2).
#
# It doesn't cover CGNAT or the Pi hardware.
{ lib, pkgs, ... }:

let
  tlsCert = pkgs.runCommand "selfSignedCerts" { buildInputs = [ pkgs.openssl ]; } ''
    openssl req \
      -x509 -newkey rsa:4096 -sha256 -days 365 \
      -nodes -out cert.pem -keyout key.pem \
      -subj '/CN=headscale' -addext "subjectAltName=DNS:headscale"

    mkdir -p $out
    cp key.pem cert.pem $out
  '';

  # Answers with the address it sees, which is the whole point of the test.
  whoami = pkgs.writeText "whoami.py" ''
    import sys
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            body = b"\0" * (20 * 1024 * 1024) if self.path == "/big" else self.client_address[0].encode()
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    ThreadingHTTPServer(("0.0.0.0", int(sys.argv[1])), Handler).serve_forever()
  '';

  whoamiService = port: {
    wantedBy = [ "multi-user.target" ];
    serviceConfig.ExecStart = "${pkgs.python3}/bin/python3 -u ${whoami} ${toString port}";
  };

  # The same over UDP on the DNS port, to show where the TV's DNS leaves from.
  dnsWhoami = pkgs.writeText "dns-whoami.py" ''
    import socket

    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    # Bound to the address queries go to. Otherwise replies leave from whichever
    # of this machine's addresses is nearest the asker, and its NAT drops them.
    s.bind(("203.0.113.1", 53))
    while True:
        _, asker = s.recvfrom(512)
        s.sendto((asker[0] + "\n").encode(), asker)
  '';

  # Stands in for SSDP, which YouTube and Netflix use to find a TV: a phone asks by
  # multicast and the TV answers it directly, and the TV announces itself by
  # multicast.
  discovery = pkgs.writeText "discovery.py" ''
    import socket, struct, sys

    GROUP, PORT, REPLY_PORT = "239.255.255.250", 1900, 1901

    def joined():
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        s.bind(("", PORT))
        group = struct.pack("4s4s", socket.inet_aton(GROUP), socket.inet_aton("0.0.0.0"))
        s.setsockopt(socket.IPPROTO_IP, socket.IP_ADD_MEMBERSHIP, group)
        return s

    def sender():
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        # Phones often send these with a TTL of 1, which no router passes on.
        s.setsockopt(socket.IPPROTO_IP, socket.IP_MULTICAST_TTL, 1)
        return s

    mode = sys.argv[1]
    if mode == "answer":
        s = joined()
        while True:
            data, asker = s.recvfrom(1024)
            if data.startswith(b"M-SEARCH"):
                s.sendto(b"HTTP/1.1 200 OK\r\n\r\n", asker)
    elif mode == "listen":
        s = joined()
        while True:
            data, announcer = s.recvfrom(1024)
            if data.startswith(b"NOTIFY"):
                print(announcer[0], flush=True)
    elif mode == "search":
        s = sender()
        s.bind(("", REPLY_PORT))
        s.settimeout(5)
        s.sendto(b"M-SEARCH * HTTP/1.1\r\n\r\n", (GROUP, PORT))
        print(s.recvfrom(1024)[1][0])
    elif mode == "announce":
        sender().sendto(b"NOTIFY * HTTP/1.1\r\n\r\n", (GROUP, PORT))
  '';

  discoveryService = mode: {
    wantedBy = [ "multi-user.target" ];
    serviceConfig = {
      ExecStart = "${pkgs.python3}/bin/python3 -u ${discovery} ${mode}";
      # Joining a multicast group fails until the machine has an address.
      Restart = "always";
      RestartSec = "2s";
    };
    unitConfig.StartLimitIntervalSec = 0;
  };

  pi = host: vlans: extra: {
    imports = [ ../modules/common.nix host extra ];
    virtualisation.vlans = vlans;
    # The first never answers: one dead address mustn't move the TV.
    tunnel.probeTargets = [ "203.0.113.9" "203.0.113.1" ];
    # Any public range will do: the secure VLAN's stand-in, which the private
    # ranges in modules/local-nets.nix don't cover.
    tunnel.extraLocalNets = [ "5.5.5.0/29" ];
    # The site's own whoami for the exit check, apart from the one the TV's checks
    # read.
    tunnel.publicIpUrl = "http://203.0.113.1:8080/";
    security.pki.certificateFiles = [ "${tlsCert}/cert.pem" ];
    environment.systemPackages = [ pkgs.conntrack-tools pkgs.curl ];
  };

  dhcpClient = {
    networking.useNetworkd = true;
    networking.useDHCP = false;
    systemd.network.networks."10-eth1" = {
      matchConfig.Name = "eth1";
      networkConfig.DHCP = "ipv4";
    };
  };
in
{
  name = "raspberry-tunnel";

  nodes = {
    # Neither Pi can share a subnet with the site. Tailscale treats connected
    # subnets as LAN: an exit node refuses to forward to them, and Pi B would
    # send that traffic around the tunnel. The real sites aren't on those subnets.
    pi-a = pi ../hosts/pi-a.nix [ 1 ] ({ config, ... }: {
      tunnel.wan = "eth1";
      # No DHCP on the fake internet.
      systemd.network.networks."10-wan" = lib.mkForce {
        matchConfig.Name = "eth1";
        address = [ "${config.networking.primaryIPAddress}/24" "192.0.2.2/24" "10.99.0.3/24" ];
        routes = [
          {
            Gateway = "192.0.2.1";
            GatewayOnLink = true;
          }
          # The secure VLAN is routed from the default VLAN, not directly connected,
          # so Pi A's firewall is all that stops the tunnel reaching it.
          {
            Destination = "5.5.5.0/29";
            Gateway = "10.99.0.2";
          }
        ];
      };
    });

    pi-b = pi ../hosts/pi-b.nix [ 2 3 ] {
      tunnel.uplink = "eth1";
      tunnel.tv = "eth2";
      tunnel.tvDns = "203.0.113.1";
    };

    # Apartment B's router. It hands out addresses, offers IPv6 as well, and NATs
    # its network onto the internet.
    apt-b-router = { config, nodes, ... }: {
      virtualisation.vlans = [ 1 2 ];
      networking.useNetworkd = true;
      networking.useDHCP = false;
      networking.firewall.enable = false;
      networking.nftables.enable = true;
      networking.nat = {
        enable = true;
        externalInterface = "eth1";
        internalInterfaces = [ "eth2" ];
      };
      systemd.network.networks."10-eth1" = {
        matchConfig.Name = "eth1";
        address = [ "${config.networking.primaryIPAddress}/24" "198.51.100.3/24" ];
        routes = [
          {
            Gateway = "198.51.100.1";
            GatewayOnLink = true;
            PreferredSource = "198.51.100.3";
          }
          # Other private networks this router can reach, as apartment A's default
          # VLAN may reach its secure VLAN while Pi B is on trial there.
          {
            Destination = "10.99.0.0/24";
            Gateway = nodes.lanhost.networking.primaryIPAddress;
          }
          {
            Destination = "5.5.5.0/29";
            Gateway = nodes.lanhost.networking.primaryIPAddress;
          }
        ];
      };
      systemd.network.networks."10-eth2" = {
        matchConfig.Name = "eth2";
        address = [ "192.168.77.1/24" "fd00:77::1/64" ];
        networkConfig = {
          DHCPServer = true;
          IPv6SendRA = true;
        };
        dhcpServerConfig = {
          PoolOffset = 100;
          PoolSize = 50;
          DNS = [ "192.168.77.1" ];
        };
        ipv6Prefixes = [ { Prefix = "fd00:77::/64"; } ];
        # Addresses only, not itself as the default route, as a router with no IPv6
        # upstream does. One that offered a dead default route would stall IPv6
        # connections until they time out, such as Tailscale's to Headscale after
        # Pi B reboots.
        ipv6SendRAConfig.RouterLifetimeSec = 0;
      };
    };

    tv = {
      imports = [ dhcpClient ];
      virtualisation.vlans = [ 3 ];
      networking.firewall.allowedTCPPorts = [ 8008 ];
      networking.firewall.allowedUDPPorts = [ 1900 ];
      systemd.services.whoami = whoamiService 8008;
      systemd.services.discovery = discoveryService "answer";
      environment.systemPackages = [ pkgs.curl ];
    };

    # On apartment B's network, and on the tailnet.
    phone = {
      imports = [ dhcpClient ];
      virtualisation.vlans = [ 2 ];
      services.tailscale.enable = true;
      security.pki.certificateFiles = [ "${tlsCert}/cert.pem" ];
      networking.firewall.allowedTCPPorts = [ 80 ];
      networking.firewall.allowedUDPPorts = [ 1900 1901 ];
      systemd.services.whoami = whoamiService 80;
      systemd.services.discovery = discoveryService "listen";
      environment.systemPackages = [ pkgs.curl ];
    };

    # A site on the internet, and the way out to it for apartment B's router. Also
    # the dashboard, a tailnet node tagged tag:dashboard, which reads the Pis'
    # status from the internet as the real one will. Being last in alphabetical
    # order, it renumbers no other node.
    web = {
      virtualisation.vlans = [ 1 ];
      networking.interfaces.eth1.ipv4.addresses = [
        {
          address = "203.0.113.1";
          prefixLength = 24;
        }
        {
          address = "198.51.100.1";
          prefixLength = 24;
        }
        {
          address = "192.0.2.1";
          prefixLength = 24;
        }
      ];
      networking.firewall.allowedTCPPorts = [ 80 8080 ];
      networking.firewall.allowedUDPPorts = [ 53 ];
      systemd.services.whoami = whoamiService 80;
      # For the Pis' exit checks. A log of its own keeps their requests out of
      # the one the TV's checks read.
      systemd.services.exit-check-site = whoamiService 8080;
      services.tailscale.enable = true;
      security.pki.certificateFiles = [ "${tlsCert}/cert.pem" ];
      environment.systemPackages = [ pkgs.curl ];
      systemd.services.dns-whoami = {
        wantedBy = [ "multi-user.target" ];
        # It binds to one of the addresses that service adds.
        wants = [ "network-addresses-eth1.service" ];
        after = [ "network-addresses-eth1.service" ];
        serviceConfig.ExecStart = "${pkgs.python3}/bin/python3 -u ${dnsWhoami}";
      };
    };

    # Stands in for apartment A's default VLAN and its secure VLAN.
    lanhost = {
      virtualisation.vlans = [ 1 ];
      networking.interfaces.eth1.ipv4.addresses = [
        {
          address = "10.99.0.2";
          prefixLength = 24;
        }
        {
          address = "5.5.5.2";
          prefixLength = 29;
        }
      ];
      networking.firewall.allowedTCPPorts = [ 80 ];
      systemd.services.whoami = whoamiService 80;
    };

    headscale = {
      virtualisation.vlans = [ 1 ];
      services.headscale = {
        enable = true;
        port = 8080;
        settings = {
          server_url = "https://headscale";
          policy.path = "${../headscale/policy.json}";
          derp = {
            server = {
              enabled = true;
              region_id = 999;
              stun_listen_addr = "0.0.0.0:3478";
            };
            urls = [ ];
          };
          dns = {
            base_domain = "tailnet";
            override_local_dns = false;
          };
        };
      };
      services.nginx = {
        enable = true;
        virtualHosts.headscale = {
          addSSL = true;
          sslCertificate = "${tlsCert}/cert.pem";
          sslCertificateKey = "${tlsCert}/key.pem";
          locations."/" = {
            proxyPass = "http://127.0.0.1:8080";
            proxyWebsockets = true;
          };
        };
      };
      networking.firewall.allowedTCPPorts = [ 80 443 ];
      networking.firewall.allowedUDPPorts = [ 3478 ];
      environment.systemPackages = [ pkgs.headscale ];
    };
  };

  testScript = ''
    import json
    import re

    FIND_WEB = "curl -fsS --max-time 5 http://203.0.113.1/"
    PI_A = "192.0.2.2"
    # The addresses of apartment B's router on the internet and on its own network.
    APT_B_ROUTER_WAN = "198.51.100.3"
    APT_B_ROUTER_LAN = "192.168.77.1"
    # The TV's connections to the site, as Pi B tracks them.
    TV_CONNECTIONS = "conntrack -L --mark 0x1/0x1 --orig-dst 203.0.113.1"
    DISCOVERY = "${pkgs.python3}/bin/python3 ${discovery}"
    # Apartment A's default VLAN and its secure VLAN.
    OTHER_NETWORKS = ("10.99.0.2", "5.5.5.2")

    def ssh_port(address):
        return f"timeout 3 bash -c '</dev/tcp/{address}/22'"

    def lan_address(machine, interface="eth1"):
        # The address field alone: DHCP leases also show a broadcast address.
        address = machine.succeed(f"ip -4 -br addr show dev {interface} | awk '{{print $3}}' | cut -d/ -f1").strip()
        assert address, f"{machine.name} has no IPv4 address on {interface}"
        return address

    def site_sees_tv_as(address, timeout=180):
        tv.wait_until_succeeds(f"{FIND_WEB} | grep -qx {address}", timeout=timeout)

    def now_on_site():
        # journalctl --since takes whole seconds. The pause leaves earlier requests out.
        return web.succeed("sleep 1; date +%s").strip()

    def site_only_saw_pi_a_since(since):
        log = f"journalctl -u whoami --since @{since}"
        web.succeed(f"{log} | grep -q {PI_A}")
        web.fail(f"{log} | grep -q {APT_B_ROUTER_WAN}")

    def tv_dns_leaves_as(address):
        for server in (APT_B_ROUTER_LAN, "9.9.9.9"):
            # dd takes the whole reply in one read. bash's read takes a byte at a time,
            # and on UDP the first byte's read throws away the rest of the reply. The
            # socket only takes replies from the server it asked.
            tv.succeed(f"bash -c 'exec 3<>/dev/udp/{server}/53; echo q >&3; timeout 5 dd bs=512 count=1 status=none <&3' | grep -qx {address}")

    def tv_and_phone_reach_each_other():
        # Each sees the other's own address, so nothing between them translates it.
        phone.wait_until_succeeds(f"curl -fsS --max-time 5 http://{tv_lan}:8008/ | grep -qx {phone_lan}", timeout=30)
        tv.wait_until_succeeds(f"curl -fsS --max-time 5 http://{phone_lan}/ | grep -qx {tv_lan}", timeout=30)

    def tv_is_kept_off_other_networks():
        for destination in OTHER_NETWORKS + (lanhost_primary,):
            tv.fail(f"curl -fsS --max-time 3 http://{destination}/")
        for address in (pi_a_tailnet, pi_b_tailnet, lan_address(pi_b, "br0")):
            tv.fail(ssh_port(address))

    # The Pis' status endpoint (D2): its fields, the CPU ticks in /proc/stat's
    # order, and the interfaces each Pi reports, by their names in these VMs.
    STATUS_FIELDS = {
        "schema", "host", "role", "time", "uptime_s", "cpu", "temp_c", "undervoltage",
        "undervoltage_events", "mem", "disk", "net", "tunnel", "public_ip", "gateway",
    }
    TUNNEL_FIELDS = {"peer", "online", "path", "relay", "rx_bytes", "tx_bytes", "latency_ms"}
    CPU_TICKS = ("user", "nice", "system", "idle", "iowait", "irq", "softirq", "steal")
    PI_A_INTERFACES = ("eth1", "tailscale0")
    PI_B_INTERFACES = ("eth1", "br0", "eth2", "tailscale0")
    UTC_TIME = re.compile(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ")

    def status_port(address):
        return f"timeout 3 bash -c '</dev/tcp/{address}/9190'"

    def timed_status(address, reader=None):
        # Asked as the dashboard asks, and how long the answer took, in seconds.
        reader = reader or web
        out = reader.succeed(f"curl -fsS --max-time 5 -w '\\n%{{time_total}}' http://{address}:9190/status")
        body, seconds = out.rsplit("\n", 1)
        return json.loads(body), float(seconds)

    def status(address, reader=None):
        return timed_status(address, reader)[0]

    def wait_for_status(address, condition, timeout=60):
        # Asks until an answer meets the condition, and returns that answer.
        answers = []

        def met(_):
            code, out = web.execute(f"curl -fsS --max-time 5 http://{address}:9190/status")
            answers.append(json.loads(out) if code == 0 else None)
            return answers[-1] is not None and condition(answers[-1])

        retry(met, timeout_seconds=timeout)
        return answers[-1]

    def paths(reply):
        return [entry["path"] for entry in reply["tunnel"] or []]

    def is_count(value):
        return type(value) is int and value >= 0

    def check_status(reply, host, role, interfaces):
        # Every field of schema 1, of its type. Only what a VM has no hardware for
        # is null.
        assert set(reply) == STATUS_FIELDS, f"{host}: {sorted(reply)}"
        assert (reply["schema"], reply["host"], reply["role"]) == (1, host, role), reply
        assert UTC_TIME.fullmatch(reply["time"]), reply["time"]
        assert is_count(reply["uptime_s"]), reply["uptime_s"]
        load, ticks = reply["cpu"]["load"], reply["cpu"]["stat"]
        assert len(load) == 3 and all(type(x) in (int, float) and x >= 0 for x in load), load
        assert list(ticks) == list(CPU_TICKS) and all(map(is_count, ticks.values())), ticks
        # A VM has no SoC sensor or Pi firmware, and nothing counts undervoltage
        # events.
        assert reply["temp_c"] is None and reply["undervoltage"] is None and reply["undervoltage_events"] is None, reply
        mem, disk = reply["mem"], reply["disk"]
        assert list(mem) == ["total_kb", "available_kb"] and 0 < mem["available_kb"] <= mem["total_kb"], mem
        assert list(disk) == ["total_b", "used_b", "avail_b"] and all(map(is_count, disk.values())), disk
        assert 0 < disk["used_b"] and disk["used_b"] + disk["avail_b"] <= disk["total_b"], disk
        assert list(reply["net"]) == list(interfaces), reply["net"]
        for counters in reply["net"].values():
            assert list(counters) == ["rx_bytes", "tx_bytes"] and all(map(is_count, counters.values())), reply["net"]
        for entry in reply["tunnel"] or []:
            assert set(entry) == TUNNEL_FIELDS, entry
        public_ip = reply["public_ip"]
        assert public_ip is None or (list(public_ip) == ["value", "checked"] and UTC_TIME.fullmatch(public_ip["checked"])), public_ip
        gateway = reply["gateway"]
        if role == "exit":
            assert gateway is None, gateway
        else:
            assert list(gateway) == ["tv_port", "tv_port_gated", "fallback"] and list(gateway["tv_port"]) == ["link", "speed_mbps"], gateway
            assert all(type(x) is bool for x in (gateway["tv_port"]["link"], gateway["tv_port_gated"], gateway["fallback"])), gateway
        return reply

    def read_sources(machine, interfaces):
        # What the endpoint reads, read on the Pi itself.
        counters = " ".join(f"/sys/class/net/{i}/statistics/rx_bytes /sys/class/net/{i}/statistics/tx_bytes" for i in interfaces)
        lines = machine.succeed(
            "date -u +%Y-%m-%dT%H:%M:%SZ; cut -d' ' -f1 /proc/uptime; head -n 1 /proc/stat; "
            f"awk '/^MemTotal:/ {{print $2}}' /proc/meminfo; stat -f -c '%S %b' /; cat {counters}"
        ).split("\n")
        date, uptime, cpu, mem_total, statfs = lines[:5]
        size, blocks = map(int, statfs.split())
        values = list(map(int, lines[5:5 + 2 * len(interfaces)]))
        return {
            "time": date,
            "uptime": float(uptime),
            "ticks": list(map(int, cpu.split()[1:9])),
            "mem_total": int(mem_total),
            "disk_total": size * blocks,
            "net": {i: values[2 * n:2 * n + 2] for n, i in enumerate(interfaces)},
        }

    def check_against_sources(machine, address, host, role, interfaces):
        # Reads the Pi's own /proc and /sys either side of a request. Each value in
        # the answer falls between the two readings, or equals what doesn't change.
        before = read_sources(machine, interfaces)
        reply = check_status(status(address), host, role, interfaces)
        after = read_sources(machine, interfaces)
        assert before["time"] <= reply["time"] <= after["time"], (before["time"], reply["time"], after["time"])
        assert int(before["uptime"]) <= reply["uptime_s"] <= after["uptime"], (before["uptime"], reply["uptime_s"])
        for n, tick in enumerate(CPU_TICKS):
            # The kernel's proc documentation warns iowait can step back, and on a
            # tickless kernel idle comes from the same accounting, so only the
            # others are held to the readings.
            if tick not in ("idle", "iowait"):
                assert before["ticks"][n] <= reply["cpu"]["stat"][tick] <= after["ticks"][n], tick
        assert reply["mem"]["total_kb"] == before["mem_total"], reply["mem"]
        assert reply["disk"]["total_b"] == before["disk_total"], reply["disk"]
        for interface in interfaces:
            for n, counter in enumerate(("rx_bytes", "tx_bytes")):
                assert before["net"][interface][n] <= reply["net"][interface][counter] <= after["net"][interface][n], (interface, counter)
        return reply

    def check_gateway_against_sources(reply):
        # Pi B's gateway fields against rule 5260, the TV's port and the gate, read
        # on Pi B just after the answer.
        gateway = reply["gateway"]
        rule = pi_b.succeed("ip rule show priority 5260").strip()
        assert gateway["fallback"] == bool(rule), (gateway, rule)
        operstate = pi_b.succeed("cat /sys/class/net/eth2/operstate").strip()
        assert gateway["tv_port"]["link"] == (operstate == "up"), (gateway, operstate)
        speed = int(pi_b.succeed("cat /sys/class/net/eth2/speed 2> /dev/null || echo -1").strip())
        assert gateway["tv_port"]["speed_mbps"] == (speed if operstate == "up" and speed > 0 else None), (gateway, speed)
        gated = pi_b.execute("systemctl is-active --quiet tv-port-gate && nft list chain bridge tv-port prerouting | grep -q 'meta broute set 1'")[0] == 0
        assert gateway["tv_port_gated"] == gated, (gateway, gated)

    def request_cost(machine, address, requests=10):
        # The dashboard's wall time per request, and the CPU time of the Pi's
        # per-request services, which systemd puts in one slice for the template.
        cpu = "awk '/^usage_usec/ {print $2}' /sys/fs/cgroup/system.slice/system-pi*status.slice/cpu.stat"
        before = int(machine.succeed(cpu))
        seconds = list(map(float, web.succeed(
            f"for i in $(seq {requests}); do curl -fsS --max-time 5 -o /dev/null -w '%{{time_total}}\\n' http://{address}:9190/status; done"
        ).split()))
        after = int(machine.succeed(cpu))
        assert len(seconds) == requests and max(seconds) < 2, seconds
        return 1000 * sum(seconds) / requests, (after - before) / 1000 / requests

    start_all()
    headscale.wait_for_unit("headscale")
    headscale.wait_for_open_port(443)
    web.wait_for_open_port(80)
    lanhost.wait_for_open_port(80)

    with subtest("both Pis and the phone join"):
        for machine in (pi_b, phone):
            machine.wait_until_succeeds(f"ip -4 route show default | grep -q 'via {APT_B_ROUTER_LAN} '")
        headscale.succeed("headscale users create tunnel")
        keys = (
            (pi_a, headscale.succeed("headscale preauthkeys create --tags tag:exit").strip()),
            (pi_b, headscale.succeed("headscale preauthkeys create --tags tag:gateway").strip()),
            (phone, headscale.succeed("headscale preauthkeys -u 1 create").strip()),
        )
        for machine, key in keys:
            machine.wait_for_unit("tailscaled")
            machine.succeed(f"tailscale login --login-server=https://headscale --auth-key={key}")
        # Logging in resets what tailscaled-set applied at boot.
        pi_a.succeed("systemctl restart tailscaled-set")
        pi_a_tailnet = pi_a.succeed("tailscale ip -4").strip()
        pi_b_tailnet = pi_b.succeed("tailscale ip -4").strip()

    with subtest("the dashboard joins the tailnet"):
        key = headscale.succeed("headscale preauthkeys create --tags tag:dashboard").strip()
        web.wait_for_unit("tailscaled")
        # Its resolver stays its own.
        web.succeed(f"tailscale login --login-server=https://headscale --auth-key={key} --accept-dns=false")

    with subtest("the TV gets its address from apartment B's router through Pi B, and no IPv6"):
        tv.wait_until_succeeds(f"ip -4 route show default | grep -q 'via {APT_B_ROUTER_LAN} '")
        tv_lan = lan_address(tv)
        phone_lan = lan_address(phone)
        # The router does offer IPv6 addresses, which the TV must not get.
        phone.wait_until_succeeds("ip -6 addr show scope global | grep -q fd00:77:")
        router_link_local = apt_b_router.succeed("ip -6 -o addr show eth2 scope link | grep -o 'fe80::[0-9a-f:]*'").strip()
        phone.succeed(f"ping -6 -c 1 -W 2 {router_link_local}%eth1")
        tv.fail(f"ping -6 -c 2 -W 2 {router_link_local}%eth1")
        print(tv.succeed("ip route; ip -6 route; ip -6 addr"))
        tv.fail("ip -6 addr show scope global | grep -q .")

    with subtest("the TV's traffic appears to come from Pi A"):
        # tv-fallback may have moved the TV before the Pis logged in.
        site_sees_tv_as(PI_A)
        tunnel_up = now_on_site()

    with subtest("the TV's DNS leaves through Pi A, whichever server it asks"):
        tv_dns_leaves_as(PI_A)

    with subtest("large transfers survive the smaller tunnel MTU"):
        # Real paths can drop these, and then only the MSS clamp saves the transfer.
        web.succeed("iptables -I INPUT -p icmp --icmp-type fragmentation-needed -j DROP")
        tv.succeed("iptables -I INPUT -p icmp --icmp-type fragmentation-needed -j DROP")
        tv.succeed("curl -fsS --max-time 60 -o /tmp/big http://203.0.113.1/big")
        tv.succeed("test $(stat -c%s /tmp/big) = 20971520")

    with subtest("phones on apartment B's network find the TV, and each reaches the other"):
        phone.wait_until_succeeds(f"{DISCOVERY} search | grep -qx {tv_lan}", timeout=30)

        def phone_heard_tv(_):
            tv.succeed(f"{DISCOVERY} announce")
            return phone.execute(f"journalctl -u discovery -o cat | grep -qx {tv_lan}")[0] == 0

        retry(phone_heard_tv, timeout_seconds=30)
        tv_and_phone_reach_each_other()
        # Each learns the TV's hardware address afresh, which needs the TV's ARP
        # replies to cross the bridge rather than be routed.
        for machine, interface in ((pi_b, "br0"), (apt_b_router, "eth2"), (phone, "eth1")):
            machine.succeed(f"ip neigh flush to {tv_lan} dev {interface}; ping -c 1 -W 2 {tv_lan}")

    with subtest("the TV can't reach other networks, the tailnet or Pi B itself"):
        FIND_APT_B_LAN = f"curl -fsS --max-time 5 http://{phone_lan}/"
        pi_b.succeed(FIND_APT_B_LAN)
        lanhost_primary = lanhost.succeed("ip -4 -o addr show eth1 | grep -o '192.168.1.[0-9]*'").strip()
        # The phone reaches them through apartment B's router, so on fallback only Pi B
        # keeps the TV off them.
        for destination in OTHER_NETWORKS + (lanhost_primary,):
            phone.succeed(f"curl -fsS --max-time 5 http://{destination}/")
        tv_is_kept_off_other_networks()

    with subtest("the phone can reach both Pis' SSH, but Pi B can't reach Pi A's"):
        phone.wait_until_succeeds(ssh_port(pi_a_tailnet), timeout=60)
        phone.wait_until_succeeds(ssh_port(pi_b_tailnet), timeout=60)
        pi_b.fail(ssh_port(pi_a_tailnet))

    with subtest("the dashboard reads both Pis' status over the tailnet, each value as its source has it"):
        for address in (pi_a_tailnet, pi_b_tailnet):
            web.wait_until_succeeds(f"curl -fsS --max-time 5 http://{address}:9190/status", timeout=60)
            # The TV's traffic has gone through the tunnel, which by now is direct.
            wait_for_status(address, lambda reply: paths(reply) == ["direct"], timeout=90)

        pi_a_status = check_against_sources(pi_a, pi_a_tailnet, "pi-a", "exit", PI_A_INTERFACES)
        # Every gateway, which here is Pi B alone: not the phone or the dashboard.
        [entry] = pi_a_status["tunnel"]
        assert (entry["peer"], entry["online"], entry["path"]) == ("pi-b", True, "direct"), entry
        # No DERP region on a direct path, and no ping from the exit node.
        assert entry["relay"] is None and entry["latency_ms"] is None, entry
        assert entry["rx_bytes"] > 0 and entry["tx_bytes"] > 0, entry

        pi_b_status = check_against_sources(pi_b, pi_b_tailnet, "pi-b", "gateway", PI_B_INTERFACES)
        [entry] = pi_b_status["tunnel"]
        assert (entry["peer"], entry["online"], entry["path"]) == ("pi-a", True, "direct"), entry
        assert entry["relay"] is None and entry["rx_bytes"] > 0 and entry["tx_bytes"] > 0, entry
        # Its disco ping to Pi A was answered.
        assert type(entry["latency_ms"]) in (int, float) and entry["latency_ms"] > 0, entry
        assert pi_b_status["gateway"]["tv_port_gated"] is True and pi_b_status["gateway"]["fallback"] is False, pi_b_status
        check_gateway_against_sources(pi_b_status)

        for path in ("/", "/status/", "/metrics"):
            code = web.succeed(f"curl -sS -o /dev/null -w '%{{http_code}}' --max-time 5 http://{pi_b_tailnet}:9190{path}")
            assert code == "404", (path, code)

    with subtest("a status request answers within 2 seconds, and its cost on each Pi is logged"):
        for host, machine, address in (("pi-a", pi_a, pi_a_tailnet), ("pi-b", pi_b, pi_b_tailnet)):
            wall_ms, cpu_ms = request_cost(machine, address)
            print(f"one status request to {host}: {wall_ms:.1f} ms for the dashboard, {cpu_ms:.1f} ms of CPU on {host}, averaged over 10")

    with subtest("the status endpoint is open only on the tailnet, and only to the dashboard and the user's own devices"):
        pi_b_lan = lan_address(pi_b, "br0")
        # Apartment B's network reaches Pi B's SSH there, but not its status.
        phone.succeed(ssh_port(pi_b_lan))
        for machine in (phone, apt_b_router):
            machine.fail(status_port(pi_b_lan))
        for address in (pi_b_lan, pi_b_tailnet):
            tv.fail(status_port(address))
        # Nor does the internet reach Pi A's, though it reaches its SSH.
        web.succeed(ssh_port(PI_A))
        web.fail(status_port(PI_A))
        # The phone, the user's own device, reads both over the tailnet.
        for address in (pi_a_tailnet, pi_b_tailnet):
            assert status(address, phone)["schema"] == 1
        # The policy grants nobody else, the Pis included.
        pi_b.fail(status_port(pi_a_tailnet))
        pi_a.fail(status_port(pi_b_tailnet))

    with subtest("the dashboard reaches the Pis' status endpoint and nothing else on the tailnet"):
        # The two Pis are its only peers.
        peers = json.loads(web.succeed("tailscale status --json"))["Peer"] or {}
        assert sorted(peer["HostName"] for peer in peers.values()) == ["pi-a", "pi-b"], [peer["HostName"] for peer in peers.values()]
        # Not their SSH, which the phone reaches.
        for address in (pi_a_tailnet, pi_b_tailnet):
            web.fail(ssh_port(address))
        # Nor the phone, whose site answers on its tailnet address.
        phone_tailnet = phone.succeed("tailscale ip -4").strip()
        phone.succeed(f"curl -fsS --max-time 5 http://{phone_tailnet}/")
        web.fail(f"curl -fsS --max-time 3 http://{phone_tailnet}/")
        web.fail(f"ping -c 1 -W 2 {phone_tailnet}")

    with subtest("both Pis' exit checks see Pi A's address, so Pi B's traffic leaves through Pi A"):
        for machine, address in ((pi_a, pi_a_tailnet), (pi_b, pi_b_tailnet)):
            machine.succeed("systemctl is-active pi-exit-check.timer")
            machine.succeed("systemctl show -P TimersMonotonic pi-exit-check.timer | grep -q 'OnUnitActiveUSec=5min'")
            machine.succeed("systemctl start pi-exit-check")
            just_now = machine.succeed("date -u -d '-60 sec' +%Y-%m-%dT%H:%M:%SZ").strip()
            public_ip = status(address)["public_ip"]
            assert public_ip is not None and public_ip["value"] == PI_A and public_ip["checked"] >= just_now, (machine.name, public_ip)

    with subtest("Pi A won't forward tunnel traffic to its LAN, though it can reach it itself"):
        for destination in ("10.99.0.2", "5.5.5.2"):
            pi_a.succeed(f"curl -fsS --max-time 5 http://{destination}/")
            pi_b.fail(f"curl -fsS --max-time 5 http://{destination}/")

    with subtest("while the tunnel works, the site only sees Pi A"):
        site_only_saw_pi_a_since(tunnel_up)

    with subtest("the TV moves to apartment B's connection if Pi A disappears, and moves back"):
        tv.succeed(FIND_WEB)
        pi_b.succeed(f"{TV_CONNECTIONS} | grep -q {pi_b_tailnet}")
        pi_a.succeed("systemctl stop tailscaled")
        site_sees_tv_as(APT_B_ROUTER_WAN)
        # A connection left over from the other path would stall instead of resetting.
        pi_b.wait_until_fails(f"{TV_CONNECTIONS} | grep -q {pi_b_tailnet}", timeout=10)
        tv_is_kept_off_other_networks()
        tv_and_phone_reach_each_other()
        # Its replies come back through Pi B even though the router could answer the
        # TV directly.
        tv_dns_leaves_as(APT_B_ROUTER_WAN)
        # On this path apartment B's router translates the TV's address, not Pi B.
        pi_b.succeed(f"{TV_CONNECTIONS} | grep -v {pi_b_tailnet} | grep -q .")
        # Pi B still answers the dashboard, within 2 seconds though its ping to Pi A
        # goes unanswered, with Pi A offline and the TV on fallback.
        wait_for_status(pi_b_tailnet, lambda reply: [entry["online"] for entry in reply["tunnel"] or []] == [False])
        reply, seconds = timed_status(pi_b_tailnet)
        assert seconds < 2, seconds
        check_status(reply, "pi-b", "gateway", PI_B_INTERFACES)
        [entry] = reply["tunnel"]
        assert (entry["peer"], entry["online"], entry["path"], entry["latency_ms"]) == ("pi-a", False, "offline", None), entry
        assert reply["gateway"]["fallback"] is True, reply["gateway"]
        check_gateway_against_sources(reply)
        # Pi B's exit check can't go around the tunnel, so it fails, and the last
        # result stays.
        last_check = reply["public_ip"]
        assert last_check is not None and last_check["value"] == PI_A, last_check
        since = pi_b.succeed("date +%s").strip()
        pi_b.succeed("systemctl start pi-exit-check")
        pi_b.succeed(f"journalctl -u pi-exit-check --since @{since} -o cat | grep -q 'site didn.t answer'")
        assert status(pi_b_tailnet)["public_ip"] == last_check
        pi_a.succeed("systemctl start tailscaled")
        site_sees_tv_as(PI_A)
        pi_b.wait_until_fails(f"{TV_CONNECTIONS} | grep -v {pi_b_tailnet} | grep -q .", timeout=10)

    with subtest("a short blip doesn't move the TV"):
        before_blip = now_on_site()
        pi_a.succeed("sysctl -w net.ipv4.ip_forward=0; sleep 8; sysctl -w net.ipv4.ip_forward=1")
        site_sees_tv_as(PI_A)
        site_only_saw_pi_a_since(before_blip)

    with subtest("the TV doesn't move if apartment B's connection itself goes down"):
        before_outage = now_on_site()
        pi_b.block()
        # Long enough for the TV to move, if it were going to.
        pi_b.succeed("sleep 30")
        pi_b.unblock()
        site_sees_tv_as(PI_A)
        site_only_saw_pi_a_since(before_outage)

    with subtest("the TV moves if Pi B's tunnel stops, and moves back"):
        pi_b.succeed("systemctl stop tailscaled")
        site_sees_tv_as(APT_B_ROUTER_WAN)
        tv_is_kept_off_other_networks()
        # Asked on Pi B itself, as the tailnet is gone: what tailscaled would have
        # said is null, and the rest is still there.
        reply = json.loads(pi_b.succeed("curl -fsS --max-time 5 http://127.0.0.1:9190/status"))
        assert reply["schema"] == 1 and reply["tunnel"] is None and reply["gateway"]["fallback"] is True, reply
        check_gateway_against_sources(reply)
        tailscale0 = pi_b.execute("ip link show tailscale0")[0] == 0
        assert all((value is not None) == tailscale0 for value in reply["net"]["tailscale0"].values()), (tailscale0, reply["net"])
        assert all(is_count(value) for value in reply["net"]["br0"].values()), reply["net"]
        pi_b.succeed("systemctl start tailscaled")
        site_sees_tv_as(PI_A)

    with subtest("Pi B's tunnel is unknown, not empty, while Tailscale is down on it, and back once it's up"):
        pi_b.succeed("tailscale down")
        # tailscaled still runs and answers, but isn't Running.
        pi_b.succeed("systemctl is-active tailscaled")
        state = json.loads(pi_b.execute("tailscale status --json")[1])["BackendState"]
        assert state != "Running", state
        # Asked on Pi B itself, as it's off the tailnet.
        reply = json.loads(pi_b.succeed("curl -fsS --max-time 5 http://127.0.0.1:9190/status"))
        assert reply["schema"] == 1 and reply["tunnel"] is None, reply
        # Its usual settings, with Pi A by address, as a stopped tailscaled can't
        # look up its name.
        pi_b.succeed(f"tailscale up --login-server=https://headscale --exit-node={pi_a_tailnet} --exit-node-allow-lan-access=true --accept-dns=false")
        reply = wait_for_status(pi_b_tailnet, lambda reply: [(entry["peer"], entry["online"]) for entry in reply["tunnel"] or []] == [("pi-a", True)])
        check_status(reply, "pi-b", "gateway", PI_B_INTERFACES)
        site_sees_tv_as(PI_A)

    with subtest("the TV moves if Pi A stays on the tailnet but stops forwarding, and moves back"):
        pi_a.succeed("sysctl -w net.ipv4.ip_forward=0")
        site_sees_tv_as(APT_B_ROUTER_WAN)
        pi_a.succeed("sysctl -w net.ipv4.ip_forward=1")
        site_sees_tv_as(PI_A)

    with subtest("everything comes back by itself after a power cut"):
        pi_a.crash()
        pi_a.start()
        site_sees_tv_as(PI_A, timeout=300)
        # Pi A stays up for this one, so the TV has no reason to use apartment B's
        # connection while Pi B boots.
        before_pi_b_power_cut = now_on_site()
        pi_b.crash()
        pi_b.start()
        site_sees_tv_as(PI_A, timeout=300)
        pi_b.wait_until_succeeds(FIND_APT_B_LAN, timeout=60)
        tv_and_phone_reach_each_other()

    with subtest("Pi B's routing survives the TV adapter being replugged"):
        # The nearest VM equivalent: the kernel removes the device, then finds it
        # again as a new link.
        pi_b.succeed("P=$(readlink -f /sys/class/net/eth2/device/..); echo 1 > $P/remove")
        pi_b.wait_until_fails("ip link show eth2", timeout=15)
        pi_b.succeed("echo 1 > /sys/bus/pci/rescan")
        pi_b.wait_until_succeeds("ip link show eth2 | grep -q 'master br0'", timeout=30)
        pi_b.wait_until_succeeds("ip link show eth2 | grep -q 'state UP'", timeout=30)
        pi_b.succeed("sleep 8")
        site_sees_tv_as(PI_A, timeout=60)
        pi_b.wait_until_succeeds(FIND_APT_B_LAN, timeout=60)
        tv_and_phone_reach_each_other()

    with subtest("Pi B's routing survives systemd-networkd restarting"):
        pi_b.succeed("systemctl restart systemd-networkd")
        pi_b.succeed("sleep 8")
        site_sees_tv_as(PI_A, timeout=60)
        pi_b.wait_until_succeeds(FIND_APT_B_LAN, timeout=60)
        tv_and_phone_reach_each_other()

    with subtest("the TV stays off apartment B's connection until tv-fallback moves it, but keeps its network"):
        # Stopped together, so the result doesn't depend on tv-fallback's timing.
        pi_b.succeed("systemctl stop tv-fallback tailscaled")
        tv.fail(FIND_WEB)
        tv_and_phone_reach_each_other()
        # Like a slow boot: the TV waits for a tunnel that comes up after tv-fallback.
        pi_b.succeed("systemctl start tv-fallback; sleep 35; systemctl start tailscaled")
        site_sees_tv_as(PI_A)

    with subtest("the TV's port goes down while Pi B's firewall is stopped, rather than skipping the tunnel"):
        pi_b.succeed("systemctl stop nftables")
        pi_b.succeed("ip link show eth2 | grep -q 'state DOWN'")
        tv.fail(FIND_WEB)
        # tv-port-gate stops with nftables, so the dashboard sees the port ungated.
        reply = wait_for_status(pi_b_tailnet, lambda reply: reply["gateway"]["tv_port_gated"] is False, timeout=10)
        assert reply["gateway"]["tv_port"]["link"] is False, reply["gateway"]
        check_gateway_against_sources(reply)
        pi_b.succeed("systemctl start nftables")
        site_sees_tv_as(PI_A, timeout=60)
        tv_and_phone_reach_each_other()
        reply = wait_for_status(pi_b_tailnet, lambda reply: reply["gateway"]["tv_port_gated"] is True, timeout=10)
        check_gateway_against_sources(reply)

    with subtest("the site saw only Pi A from Pi B's power cut on, and never anything but Pi A or apartment B's connection"):
        site_only_saw_pi_a_since(before_pi_b_power_cut)
        web.fail(f"journalctl -u whoami -o cat | grep -E '^[0-9.]+ ' | grep -v -e '^{PI_A} ' -e '^{APT_B_ROUTER_WAN} ' | grep -q .")

    with subtest("the dashboard sees TV B's port ungated once its bridge rules are gone, and gated once they're back"):
        pi_b.succeed("nft delete table bridge tv-port")
        # tv-port-gate, still running, finds the rule gone and takes the port down.
        wait_for_status(pi_b_tailnet, lambda reply: reply["gateway"]["tv_port_gated"] is False, timeout=10)
        pi_b.succeed("systemctl is-active tv-port-gate")
        pi_b.wait_until_succeeds("ip link show eth2 | grep -q 'state DOWN'", timeout=10)
        reply = wait_for_status(pi_b_tailnet, lambda reply: reply["gateway"]["tv_port"]["link"] is False, timeout=10)
        check_gateway_against_sources(reply)
        # Loading Pi B's tables again brings the rule back, then the port and the
        # TV's tunnel.
        pi_b.succeed("systemctl reload nftables")
        reply = wait_for_status(
            pi_b_tailnet, lambda reply: reply["gateway"]["tv_port_gated"] is True and reply["gateway"]["tv_port"]["link"] is True, timeout=30
        )
        check_gateway_against_sources(reply)
        site_sees_tv_as(PI_A, timeout=60)

    with subtest("the Pi A check above really depends on Pi A's firewall"):
        pi_a.succeed("nft delete table inet tunnel-exit")
        # Seen from Pi A's address, so through the tunnel rather than apartment B's
        # router.
        pi_b.wait_until_succeeds("curl -fsS --max-time 5 http://5.5.5.2/ | grep -qx 10.99.0.3", timeout=30)

    with subtest("the TV's port goes down if its bridge rules disappear, which the TV checks above depend on"):
        pi_b.succeed("nft delete table bridge tv-port")
        pi_b.wait_until_succeeds("ip link show eth2 | grep -q 'state DOWN'", timeout=10)
        tv.fail(FIND_WEB)
        # With the port forced up regardless, the TV goes around the tunnel.
        pi_b.succeed("systemctl stop tv-port-gate; ip link set eth2 up")
        site_sees_tv_as(APT_B_ROUTER_WAN, timeout=60)
        tv.wait_until_succeeds(f"ping -6 -c 1 -W 2 {router_link_local}%eth1", timeout=30)
  '';
}
