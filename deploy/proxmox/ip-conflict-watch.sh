#!/usr/bin/env bash
# Detect an IP conflict on the Triangle DB tier.
#
# The DB hosts' addresses are static in Proxmox and inside the containers, but
# they were taken from Drexel's DHCP pool and were never excluded from it
# (central DHCP lives on 10.254.5.41; Proxmox is not the DHCP server, so nothing
# on this host can reserve them). Our hosts therefore keep their addresses, but
# nothing stops that server leasing the same address to some other device once
# the old leases lapse. This cannot prevent that -- it detects it quickly.
#
# Runs on the PROXMOX HOST, which shares a bridge with the containers. ARP is
# link-local, so this does not work from a routed workstation.
#
# Exit: 0 all good, 1 conflict (a foreign MAC answered), 2 a host did not answer.
set -uo pipefail

IFACE="${IFACE:-vmbr0}"
TAG="triangle-ip-watch"

# ip=expected-mac. Keep in sync with `pct config <id> | grep net0`.
WATCH=(
  "10.248.40.154=bc:24:11:e5:cc:58"  # THETRIANGLE-DB1-LXC   (CT 108, MariaDB)
  "10.248.40.183=bc:24:11:83:05:ea"  # THETRIANGLE-MAXSCALE  (CT 109)
)

# Optional overrides, e.g. to add a host without editing this file.
# shellcheck source=/dev/null
[[ -r /etc/triangle-ip-watch.conf ]] && source /etc/triangle-ip-watch.conf

log() { logger -t "$TAG" -p "$1" -- "$2"; echo "[$1] $2"; }

status=0

for entry in "${WATCH[@]}"; do
  ip="${entry%%=*}"
  expected="$(tr '[:upper:]' '[:lower:]' <<<"${entry#*=}")"

  # -c 3 -w 3: three probes, give up after three seconds either way.
  out="$(arping -c 3 -w 3 -I "$IFACE" "$ip" 2>/dev/null)"
  macs="$(grep -oiE '([0-9a-f]{2}:){5}[0-9a-f]{2}' <<<"$out" | tr '[:upper:]' '[:lower:]' | sort -u)"

  if [[ -z "$macs" ]]; then
    # Not a conflict: the container is probably down, or the bridge is wrong.
    # Worth surfacing, but it must not read as "someone stole the address".
    log daemon.warning "$ip did not answer ARP on $IFACE (host down, or wrong interface?)"
    [[ $status -eq 0 ]] && status=2
    continue
  fi

  foreign="$(grep -v "^${expected}$" <<<"$macs" || true)"
  if [[ -n "$foreign" ]]; then
    # Two devices answering for one address. On a database host this shows up
    # as intermittent, inexplicable connection failures rather than a clean
    # outage, so it is worth an alarm rather than a warning.
    log daemon.err "IP CONFLICT on $ip: expected $expected, also answered by: $(tr '\n' ' ' <<<"$foreign")"
    status=1
  else
    log daemon.info "$ip OK ($expected)"
  fi
done

exit "$status"
