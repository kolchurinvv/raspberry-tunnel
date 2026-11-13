# D1: Pi B bridges TV B onto its apartment's network and routes only what the TV sends to single addresses

- **Date:** 2026-11-07
- **Status:** accepted
- **Decided by:** the user chose to bridge the TV for normal casting. The agent
  designed how it works (`broute`, the DNS redirect, the ct mark, the
  private-range blackholes, and `tv-port-gate`).

## Context

Pi B used to put TV B on a network of its own, `10.77.0.0/24`, with its own DHCP
server and NAT, and its firewall kept the TV off every private range. On real
hardware the TV went online, but a phone on the apartment's Wi-Fi couldn't cast
to it. Casting needs three things that design ruled out:

- Phones find TVs by multicast: SSDP for YouTube and Netflix on the TVs
  involved, mDNS for AirPlay. Multicast doesn't cross a router.
- The phone then connects to the TV's own address, which the apartment's router
  didn't know how to reach.
- The TV has to answer the phone, which the firewall dropped.

The options considered:

- YouTube's "Link with TV code", which pairs through YouTube's servers. No change
  needed, but it covers YouTube only.
- Discovery relays on Pi B, plus a static route to `10.77.0.0/24` on each
  apartment's router. It depends on apartment B's combo unit supporting static
  routes, and the route sends traffic back out the router's LAN port, so the two
  directions take different paths, which router firewalls can drop.
- Discovery relays plus proxy ARP, with the TV on an address reserved on each
  apartment's network. It needs that address set up per apartment, and an SSDP
  relay nixpkgs doesn't have.
- A bridge, which the user proposed ("have the pi be a 'bridge-mode router'") and
  chose after asking for normal casting rather than the TV code.

## Decision

`hosts/pi-b.nix` puts the uplink and the TV's port into one bridge, `br0`, which
carries Pi B's own address. The TV gets its address from the apartment's router.

A bridge-family nftables table, `tv-port`, does the routing part with `meta
broute`. Every IPv4 packet the TV sends to a single address goes up to Pi B's
routing instead of across the bridge, and arrives on the TV's port, so `iif tv0`
rules still select it. Routing then sends it to:

- the TV's own network, through the `suppress_prefixlength 0` rule at 5255;
- a blackhole, at 5257, for every other private range in
  `modules/local-nets.nix`;
- the tunnel, through Tailscale's table at 5270;
- this apartment's connection while `tv-fallback` has added its rule at 5260;
- nowhere, through the blackhole at 5280.

Broadcast, multicast and ARP cross the bridge untouched. ARP has to: if it went up
to routing, the TV's replies would arrive on its port, where nothing asked, and be
dropped. The TV's port carries only IPv4 and ARP.

Pi B masquerades only what goes into the tunnel, and marks the TV's connections
with ct mark `0x1` to recognise them. On this apartment's connection the router
does the NAT, so phones see the TV's own address. The one exception is the
redirected DNS, which Pi B also masquerades there. Otherwise the router's replies
would cross the bridge straight to the TV, from the resolver's address rather
than the one the TV asked, and the TV would drop them. DNS from the TV goes to
`tunnel.tvDns`, whichever server the TV asked.

The `tv-port-gate` service brings the TV's port up only while the `tv-port` table
is loaded with its broute rule, checking every 2 seconds. Ordered after nftables and bound to it, it
takes the port down before nftables removes its tables, on stop or at shutdown,
and never starts when the load fails at boot. The port's networkd activation
policy is `manual`, so nothing else brings it up.

## Consequences

- The TV can reach its own apartment's network, and the network can reach it, like
  any other TV there. The rule 4 ratchet was loosened for this, with the user's
  approval. Pi B still keeps the TV out of every other private range on both
  paths, apart from routes the router hands out over DHCP (below), and out of
  the tailnet and Pi B itself.
- Pi B needs no per-apartment settings: it learns the network from DHCP.
- Phones on another network or VLAN than Pi B's uplink won't find the TV. During
  the trial in apartment A, the TV's network is the default VLAN.
- Anything plugged into the TV's port is treated as the TV, and a cable from it
  back to the apartment's network would loop the network, as STP is off.
- Pi B's address comes from `br0`'s MAC address, not the built-in port's.
- Replies to a phone's connection reach the phone through Pi B, while the phone's
  side crosses the bridge, so Pi B's forward rule for the TV's own network can't
  depend on connection state.
- The TV's port has no IPv4 address, and the kernel's reverse path filter drops
  packets arriving on such a port even in loose mode, which systemd's
  `50-default.conf` sets on every interface. So the port turns it off, and only
  NixOS's own reverse path check in nftables applies there.
- Unicast from the TV to its own network is routed, so its TTL drops by one. mDNS
  replies sent by unicast arrive with 254 rather than 255. Current clients check
  the source address instead, but an old client that checks TTL would drop them.
- Rule 5255 accepts any route in Pi B's main table other than the default. That
  includes classless static routes the apartment's router hands out over DHCP,
  which would bypass the private-range blackholes.
- If nftables isn't running, the TV is offline rather than leaking past the
  tunnel. After a manual `systemctl stop nftables`, starting it brings the port
  back. If the table goes missing or its broute rule is flushed while nftables
  stays active, the TV can bypass the tunnel for up to 2 seconds before the gate
  takes the port down.
- `tv-port-gate` polls rather than following the adapter's device unit. A
  device-bound version missed an adapter that came back while it was still
  stopping, as after a quick reset, and left the port down.

## Enforcement

`tests/tunnel.nix` covers, each in its own subtest:

- the TV's address, and that it gets no IPv6;
- its DNS;
- discovery and connections both ways with a phone;
- ARP resolution of the TV from Pi B, the router and the phone;
- what the TV can't reach on both paths, including private networks that
  apartment B's router can reach;
- the TV's port going down while nftables is stopped, and coming back up after
  the adapter is replugged;
- the final subtest, which shows the port going down once `tv-port` is deleted,
  and the TV leaking to apartment B's connection if the port is forced up
  without it.
