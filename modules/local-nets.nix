# Destinations tunnelled traffic may never reach: private ranges and the tailnet.
# A site's networks on public address space aren't covered by these, and go in
# tunnel.extraLocalNets instead.
[
  "10.0.0.0/8"
  "100.64.0.0/10"
  "169.254.0.0/16"
  "172.16.0.0/12"
  "192.168.0.0/16"
]
