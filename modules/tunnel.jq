# Builds the "tunnel" list of the reply to GET /status, schema 1, from
# tailscaled's answers. modules/status.nix builds the rest of the reply in bash.
# The contract, field by field, is in
# docs/decisions/D2-public-dashboard-pulls-from-the-pis.md.
#
# The input is tailscaled's answer to the status request, the whole HTTP/1.0
# response as its local API sent it. $ping is its answer to the disco ping, the
# same way, or empty, and $ping_ip the address pinged. $role is "exit" or
# "gateway". A value that is missing or malformed becomes null: the reply never
# guesses one.

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
| ($ping | local_api_json) as $pong
| if $role != "exit" and $role != "gateway" then error("unknown role") else . end
| if ($tailscale | type) != "object" then null
  else
    ($tailscale.Peer | if type == "object" then [.[]] else [] end) as $peers
    | if $role == "gateway" then [$peers[] | select(.ExitNode == true) | tunnel_entry($pong)]
      else [$peers[]
          | select(.Tags | if type == "array" then any(.[]; . == "tag:gateway") else false end)
          | tunnel_entry(null)]
        | sort_by(.peer)
      end
  end
