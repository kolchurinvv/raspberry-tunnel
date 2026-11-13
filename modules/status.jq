# Builds the reply to GET /status, schema 1, from what modules/status.nix's
# script read. The contract, field by field, is in
# docs/decisions/D2-public-dashboard-pulls-from-the-pis.md.
#
# The input is tailscaled's answer to the status request, the whole HTTP/1.0
# response as its local API sent it. Everything else comes in as a named
# argument, all of them always passed, as text. A value that is empty or
# malformed becomes null: the reply never guesses one.

# Text read from /proc, /sys or a command.
def count: if type == "string" and test("^[0-9]+$") then tonumber else null end;
def decimal: if type == "string" and test("^[0-9]+(\\.[0-9]+)?$") then tonumber else null end;
def flag: if . == "1" then true elif . == "0" then false else null end;
def words: [splits(" +") | select(. != "")];

# Values in tailscaled's JSON, which may be missing or of another type.
def text: if type == "string" then . else null end;
def boolean: if type == "boolean" then . else null end;
def counter: if type == "number" and . >= 0 and . == floor then . else null end;
def filled: type == "string" and . != "";

# The JSON in an HTTP response from tailscaled's local API, or null unless it
# answered 200 with JSON.
def local_api_json:
  if type == "string" and test("^HTTP/1\\.[01] 200 ") then
    index("\r\n\r\n") as $end
    | if $end == null then null else .[$end + 4:] | try fromjson catch null end
  else null end;

def ipv4: type == "string" and test("^((25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])\\.){3}(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])$");
def utc_time: type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$");

# The disco ping's round trip in milliseconds, if $pong is the answer from the
# address the script pinged and that address is this peer's.
def latency_ms($pong):
  if ($pong | type) == "object"
    and ($pong.Err | text) == ""
    and ($pong.IP | text) == $ping_ip
    and (.TailscaleIPs | if type == "array" then any(.[]; . == $ping_ip) else false end)
    and ($pong.LatencySeconds | type) == "number"
    and $pong.LatencySeconds > 0
  then $pong.LatencySeconds * 10000 | round / 10
  else null end;

# One peer in tailscaled's status, as an entry of the reply's tunnel list.
# The path follows magicsock: CurAddr is set only while a direct path is in use,
# PeerRelay while a peer relay is, and Active means this node sent to the peer
# in the last 45 seconds. Relay is the peer's home DERP region, set whatever the
# path, so it's only reported while the path is DERP.
def tunnel_entry($pong):
  (.Online | boolean) as $online
  | (if $online == null then null
     elif $online | not then "offline"
     elif .CurAddr | filled then "direct"
     elif .PeerRelay | filled then "peer_relay"
     elif .Active == true then "derp"
     elif .Active == false then "idle"
     else null end) as $path
  | {
      peer: (.HostName | text),
      online: $online,
      path: $path,
      relay: (if $path == "derp" and (.Relay | filled) then .Relay else null end),
      rx_bytes: (.RxBytes | counter),
      tx_bytes: (.TxBytes | counter),
      latency_ms: (if $pong == null then null else latency_ms($pong) end)
    };

local_api_json as $tailscale
| (if ($tailscale | type) != "object" then null
   else $tailscale.Peer | if type == "object" then [.[]] else [] end
   end) as $peers
| ($ping | local_api_json) as $pong
| ($cpu | words | if .[0] == "cpu" then .[1:] else [] end) as $ticks
| ($loadavg | words) as $load
| ($disk | words | map(count)) as $statfs
| ($public_ip | words) as $cached
| if $role != "exit" and $role != "gateway" then error("unknown role \($role)") else . end
| {
    schema: 1,
    host: $host,
    role: $role,
    time: $time,
    uptime_s: ($uptime | words | .[0] | decimal | if . == null then null else floor end),
    cpu: {
      load: [$load[0], $load[1], $load[2] | decimal],
      stat: {
        user: ($ticks[0] | count),
        nice: ($ticks[1] | count),
        system: ($ticks[2] | count),
        idle: ($ticks[3] | count),
        iowait: ($ticks[4] | count),
        irq: ($ticks[5] | count),
        softirq: ($ticks[6] | count),
        steal: ($ticks[7] | count)
      }
    },
    # Millidegrees, to one decimal place.
    temp_c: ($temp | if type == "string" and test("^-?[0-9]+$") then tonumber / 100 | round / 10 else null end),
    undervoltage: ($undervoltage | flag),
    # Nothing counts these without privilege: see modules/status.nix.
    undervoltage_events: null,
    mem: {
      total_kb: ($mem_total | count),
      available_kb: ($mem_available | count)
    },
    # statvfs: fragment size, blocks, free blocks, blocks free to non-root.
    disk: (
      $statfs as [$size, $blocks, $free, $available]
      | {
          total_b: (if $size and $blocks then $blocks * $size else null end),
          used_b: (if $size and $blocks and $free then ($blocks - $free) * $size else null end),
          avail_b: (if $size and $available then $available * $size else null end)
        }
    ),
    net: (
      $net | split("\n") | map(words | select(length > 0)
        | {key: .[0], value: {rx_bytes: (.[1] | count), tx_bytes: (.[2] | count)}})
      | from_entries
    ),
    tunnel: (
      if $peers == null then null
      elif $role == "gateway" then [$peers[] | select(.ExitNode == true) | tunnel_entry($pong)]
      else [$peers[]
          | select(.Tags | if type == "array" then any(.[]; . == "tag:gateway") else false end)
          | tunnel_entry(null)]
        | sort_by(.peer)
      end
    ),
    public_ip: (
      if ($cached[0] | utc_time) and ($cached[1] | ipv4)
      then {value: $cached[1], checked: $cached[0]}
      else null end
    ),
    gateway: (
      if $role == "gateway" then {
        tv_port: {
          link: ($tv_link | flag),
          speed_mbps: ($tv_speed | count | if . == 0 then null else . end)
        },
        tv_port_gated: ($tv_port_gated | flag),
        fallback: ($fallback | flag)
      } else null end
    )
  }
