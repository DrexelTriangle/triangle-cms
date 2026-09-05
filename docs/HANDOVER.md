# Delta deployment notes

Historical observations from August 2026, with the September media ACL fix.
These are not a current inventory of the live infrastructure.

Current procedures:
- [Deployment, media permissions, and rollback](../deploy/README.md)
- [ETL rebuild and verification](ETL-REBUILD.md)
- [Local development](../README.md)
- Infrastructure configuration lives in `triangle-infrastructure`.

## Recorded topology

| Host | Address | Role |
| --- | --- | --- |
| Delta, VM 105 | `10.248.40.168` | Blue/green CMS, nginx, CephFS media, Actions runner |
| DB1, CT 108 | `10.248.40.154` | MariaDB primary |
| MaxScale, CT 109 | `10.248.40.183:4006` | CMS database proxy |
| WordPress, VM 100 | `10.248.40.141` | Export and media source |

The recorded public endpoint was `https://delta.thetriangle.org`.
Check current infrastructure configuration before using these addresses.

## Media sync

Push from the WordPress host; Delta did not have a key to pull.
Adjust the year to the media being copied.

```bash
ssh tadmin@10.248.40.141 'rsync -a --no-owner --no-group --chmod=D775,F664 \
  --exclude="*.php" --exclude="*.exe" --exclude="*.sh" \
  /var/www/html/thetriangle.org/wp-content/uploads/2026/ \
  tadmin@10.248.40.168:/mnt/cephfs/media/wp-content/uploads/2026/'
```

Keep `D775`: chmod sets the ACL mask from the group bits. `D755` removes
effective write permission from the backend's uid 10001, breaking uploads
when it next creates a monthly directory. Verify the mask after a sync:

```bash
getfacl -pc /mnt/cephfs/media/wp-content/uploads/2026 | grep -E '10001|mask::'
```

Expect `user:10001:rwx` without an effective-permission restriction and
`mask::rwx`. The infrastructure `delta-host.yml` playbook repairs permissions.
Investigate rsync exit 23; a directory timestamp failure alone does not imply
missing files. Check origin files before diagnosing cached public 404s.

## Historical infrastructure concerns

Confirm their current status in `triangle-infrastructure`:

- Backups and replication were absent on August 1. Do not infer current
  recovery coverage from this note; verify both before a destructive rebuild.
- Delta mounted CephFS with `client.admin`. Its Docker-enabled runner could
  access host credentials. The proposed replacement was a media-scoped CephX
  identity (`ceph fs authorize cephfs-pve3 client.triangle-media /media rw`),
  followed by removal of the cluster-admin key from Delta.
- DB1 and MaxScale used static addresses inside Drexel's DHCP pool. The
  collision risk was accepted then; investigate ARP on intermittent failures.
  DB1's recorded MAC was `bc:24:11:e5:cc:58`.
- Delta's root filesystem grew to 30 GiB on August 2, consuming the remaining
  volume-group space. Further growth requires additional storage.
