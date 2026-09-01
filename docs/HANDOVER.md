# Triangle CMS — Delta handover

State of the Delta deployment as of **2026-08-01**, what to watch out for, and
what is still open. Written for someone taking over operation of the system.

Companion docs: the [wiki](https://github.com/DrexelTriangle/triangle-cms/wiki)
has the API reference and data models; `README.md` covers local development.

---

## 1. What is running where

| Host | Address | Role |
|---|---|---|
| `thetriangle-delta` (VM 105) | `10.248.40.168` | CMS backend + frontend (blue/green), host nginx, CephFS media, GitHub Actions runner |
| `THETRIANGLE-DB1-LXC` (CT 108) | `10.248.40.154` | MariaDB 11.8 primary |
| `THETRIANGLE-MAXSCALE` (CT 109) | `10.248.40.183` | MaxScale 24.02 proxy, port **4006** |
| `thetriangle-wordpress` (VM 100) | `10.248.40.141` | Legacy WordPress — source of media and exports |

Public entry point is **https://delta.thetriangle.org** via Cloudflare Tunnel.
The backend reaches the database through MaxScale (`DB_HOST=10.248.40.183`,
`DB_PORT=4006`), never DB1 directly.

Deploys are blue/green: merging to `main` triggers the runner, which pulls new
GHCR images into the idle slot and flips `active-upstreams.conf`. Check the live
slot with `sudo cat /etc/nginx/triangle-cms/active-upstreams.conf`.

Access is `ssh tadmin@<host>` with key auth. **`tadmin` has `(ALL) NOPASSWD: ALL`
on all three hosts**, so root operations need no password — verify with
`sudo -n -l`. The nginx-only NOPASSWD entries on Delta
(`nginx -t`, `nginx -s reload`) belong to the **`triangle-runner`** service
account, not to `tadmin`; do not confuse the two.

A password is still required for SSH *password* login (`PasswordAuthentication`
is on), the Proxmox VM console, and `su -` to root — that last one is root's own
credential, not `tadmin`'s.

---

## 2. Current data state

Loaded 2026-08-01 from a WordPress export taken the same day, and verified
checksum-identical to the ETL output (see §4).

| | |
|---|---|
| articles | 10,058 (`MIN(id) = 0`) |
| authors | 875 |
| article ↔ author links | 7,668 |
| embeddings | 10,058 (1:1 with articles) |
| comments | 1,540 |
| taxonomy rows | 36 (7 sections + 29 subsections) |

Integrity: 0 orphan links, 0 duplicate links, 0 articles whose byline disagrees
with the linked author, 0 mojibake, 0 `photo_url` sentinels. All 6,757 featured
images point at `delta.thetriangle.org`.

`cms_users` starts empty by design — the first OIDC login creates and promotes
the account (`CMS_AUTO_PROMOTE_ALL_ADMINS=true`).

### Things that look like bugs but are not

- **2,623 published articles have no byline.** They genuinely have no author in
  WordPress, mostly pre-2019. This is the source data, not a migration failure.
- **22 featured images 404.** They are absent from the WordPress origin too —
  they 404 on the live WP site. Nothing to recover.
- **`site_taxonomy` counts and section listings both come from the same
  matcher**, so they agree. If you add a section, its slug must match the
  category text in the articles (see §5).

---

## 3. Rebuilding the data (ETL → reseed)

Everything below runs from a `wordpress-etl` checkout on a workstation, not on
the servers.

### 3.1 Getting an export

WordPress does not produce a zip — you build one from **two separate exports**:

1. **Tools → Export → All content** (or Posts) → save as `wp-posts.xml`
2. **Tools → Export →** the Co-Authors Plus **Guest Authors** post type → save
   as `wp-guestAuths.xml`

Then:

```bash
mkdir -p wp-export
mv <posts>.xml       wp-export/wp-posts.xml
mv <guestauths>.xml  wp-export/wp-guestAuths.xml
zip -r wp-export.zip wp-export
mv wp-export.zip wordpress-etl/Data/wp-export.zip
```

The path is hardcoded (`Utils/Constants.py`). Members are matched by filename
prefix and the match is **case-sensitive** — `wp-guestAuths`, capital A.

Do **not** reuse the All-content export for both members: the guest-author
parser does not filter by post type and would run every post through the author
path.

### 3.2 Running the pipeline

```bash
cd wordpress-etl
MEDIA_BASE_URL=https://delta.thetriangle.org \
  .venv/bin/python main.py --generate-embeddings --best-guess --headless
```

Takes a few minutes; embeddings are ~1 minute for 10k articles. `--headless`
requires `--best-guess`.

`MEDIA_BASE_URL` is baked into `photo_url` **and** into inline `<img>` tags in
article bodies. It only changes on a re-run — editing the env var without
reseeding leaves every image pointing at the old host.

### 3.3 Loading into the database

```bash
cd logs/sql
for f in authors articles articles_authors seo article_embeddings comments; do
  gzip -c $f.sql | ssh tadmin@10.248.40.154 \
    'gunzip | sudo -n mariadb --default-character-set=utf8mb4 triangle'
done
```

Order matters. Each file carries its own `CREATE TABLE`, so `DROP DATABASE
triangle; CREATE DATABASE triangle CHARACTER SET utf8mb4 COLLATE
utf8mb4_uca1400_ai_ci;` first is safe.

> **`--default-character-set=utf8mb4` is not optional.** DB1's client charset
> defaults to **latin1**, so a bare `mariadb` double-encodes every multibyte
> character — `“` is stored as `â€œ` across the whole corpus. Worse, reading it
> back through the same bare client converts it a second time and prints a
> correct-looking `“`, so the obvious check passes. Verify with
> `SELECT HEX(...)` and expect `E2809C`.

Then:

1. **Restart the backends** — `site_taxonomy` and the other CMS tables are
   created at runtime by the Go code, so restoring taxonomy before this fails
   with `Table 'triangle.site_taxonomy' doesn't exist`.
2. **Restore `site_taxonomy`** from a dump (it is CMS config, not ETL output —
   a reseed wipes it, and empty taxonomy makes every section page 404).
3. **Restart again** so the counts rebuild.

```bash
ssh tadmin@10.248.40.168 'docker restart triangle-cms-backend-blue-1 triangle-cms-backend-green-1'
ssh tadmin@10.248.40.154 'sudo -n mariadb --default-character-set=utf8mb4 triangle' < taxonomy.sql
ssh tadmin@10.248.40.168 'docker restart triangle-cms-backend-blue-1 triangle-cms-backend-green-1'
```

### 3.4 Always check for errors

`mariadb` prints nothing on success, and a failure in one file does not stop the
others. **Grep the output for `^ERROR` — do not pipe it through `head`.** A
failed `authors` load with everything else succeeding looks almost normal, and
leaves every link pointing at authors that were never inserted.

---

## 4. Verifying a reseed

Load the ETL output into a scratch database and compare rather than eyeballing:

```sql
CREATE DATABASE triangle_verify CHARACTER SET utf8mb4 COLLATE utf8mb4_uca1400_ai_ci;
-- load the same six files into it, then per table:
SELECT BIT_XOR(CRC32(CONCAT_WS(char(1), col1, COALESCE(col2,char(2)), ...))) FROM triangle.<t>;
SELECT BIT_XOR(CRC32(CONCAT_WS(char(1), col1, COALESCE(col2,char(2)), ...))) FROM triangle_verify.<t>;
```

`BIT_XOR(CRC32(...))` is order-independent and has no `GROUP_CONCAT` length
limit. Sentinel-encode NULLs so they cannot collide with empty strings. Drop the
scratch database afterwards.

Expect `articles` to have **four columns the ETL does not emit** —
`archived_at`, `focus_keyword`, `meta_description`, `seo_title` — added at
startup by `EnsureArticlesSchema` and backfilled from the Yoast data in `seo`.
Exclude them from the comparison.

Useful integrity queries:

```sql
-- links pointing at an author that does not exist
SELECT COUNT(*) FROM articles_authors aa
  LEFT JOIN authors au ON au.id = aa.author_id WHERE au.id IS NULL;

-- articles credited to the WRONG person (no constraint catches this)
SELECT COUNT(DISTINCT ar.id) FROM articles ar
  JOIN articles_authors aa ON aa.articles_id = ar.id
  JOIN authors au ON au.id = aa.author_id
 WHERE au.display_name IS NOT NULL
   AND ar.authors NOT LIKE CONCAT('%', au.display_name, '%');
```

---

## 5. Gotchas worth knowing

**The ETL keeps decision caches under `logs/`.** `auth_conflicts.json` (author
merges) and `article-sanitizer/article_author_resolution_cache.json` (byline →
author) persist answers across runs so unattended runs do not stop on prompts.
Both used to store WordPress author **ids**, which are renumbered *and reused*
between exports — that silently credited articles to the wrong person. They now
re-resolve by name and heal themselves, but **if bylines ever look wrong after a
fresh export, these caches are the first place to look.**

**Never rename a section slug.** `/comics-puzzles` is hardcoded in Scalene
(`Header.astro`, `SideMenu.astro`, `index.astro`, `siteConfig.ts`) *and* in the
CMS (`handlers.go`, homepage blocks). Renaming the slug 404s the section page
and empties its homepage block. To make a section match different category text,
add a **subsection** whose slug matches instead — a section matches itself OR
any of its children.

**Cloudflare caches 404s for up to 4 hours.** A newly added image can 404
publicly while the origin serves it fine. Diagnose with
`curl -sI` and read `cf-cache-status` / `age`; `?v=<ts>` bypasses it. Always
check the origin (`ssh delta` → `curl -sI http://localhost/wp-content/...`)
before concluding a file is missing.

**Media lives on CephFS and is synced from WordPress by hand.** New images
added to WP after the last sync will 404. The rsync must be **pushed from**
`10.248.40.141` — Delta holds no key to pull:

```bash
ssh tadmin@10.248.40.141 'rsync -a --no-owner --no-group --chmod=D775,F664 \
  --exclude="*.php" --exclude="*.exe" --exclude="*.sh" \
  /var/www/html/thetriangle.org/wp-content/uploads/2026/ \
  tadmin@10.248.40.168:/mnt/cephfs/media/wp-content/uploads/2026/'
```

**`D775`, not `D755` — the group bit is load-bearing.** The CMS backend runs as
uid 10001 and owns none of this tree, so its write access comes from a POSIX ACL
(`user:10001:rwx` plus the `default:` entries new directories inherit). A chmod
on an ACL'd directory sets the **ACL mask** from the mode's group bits, so
`D755` silently clamps that entry to `#effective:r-x`. `getfacl` still prints
`user:10001:rwx` and `ls` shows only a `+`, so it reads as correct.

Uploads then keep working until the 1st of the next month, when the handler
first has to create a new `YYYY/MM` directory and can't — `POST /v1/media`
returns 500 on **every** upload with `mkdir .../uploads/YYYY/MM: permission
denied` in the backend log. That is exactly the 2026-09-01 outage.

`D775` keeps the mask at `rwx` and matches the `0775` the CMS itself creates
month directories with. Verify after any sync — the mask, not the entry, is
what decides:

```bash
getfacl -pc /mnt/cephfs/media/wp-content/uploads/2026 | grep -E '10001|mask::'
# want: user:10001:rwx  (no "#effective:") and mask::rwx
```

`ansible-playbook playbooks/delta-host.yml` in `triangle-infrastructure` repairs
a clamped mask, but only on the next run — the fix belongs in the command above.

It exits **23** with "failed to set times" on one directory. That is a
directory mtime owned by another uid, not a data failure — ignore it.

**Merged PRs do not always contain what you think.** Twice during this work a PR
was auto-closed as "merged" against another branch's merge commit, leaving later
commits behind. After merging anything important:

```bash
git fetch origin && git merge-base --is-ancestor <sha> origin/main && echo IN MAIN
```

---

## 6. Open items

### 6.1 No automated backups, and DB1 is the only copy — **highest priority**

There is no replica (DB2 was never provisioned) and no scheduled dump: no cron
entry, no systemd timer. The only backups are manual dumps in
`~tadmin/` on DB1, taken by hand. Losing DB1 loses everything since the last one.

A nightly dump is a few lines; a replica is the real fix.

### 6.2 Delta's root filesystem — RESOLVED 2026-08-02

`/` was 15 GB with ~4.7 GB free (67%), which is tight for blue/green: it keeps
two slots plus old tags, and 54 images were on disk with only 4 in use.

**Done — the volume was grown.** The volume group turned out to be only half
allocated (`VSize <30.00g`, `VFree 15.00g`), so 15 GB of unused extents were
already attached to the VM. No Proxmox change and no reboot were needed; ext4
resizes while mounted:

```bash
sudo lvextend -l +100%FREE /dev/ubuntu-vg/ubuntu-lv
sudo resize2fs /dev/ubuntu-vg/ubuntu-lv
```

| | before | after |
|---|---|---|
| LV size | 15.00 GiB | 30.00 GiB |
| VG free extents | 15.00 GiB | 0 |
| `/` free | 4.7 GB (67% used) | 19 GB (34% used) |

Filesystem state `clean` afterwards, all four containers stayed healthy, and
`/healthz`, `/v1/health/db` and `/v1/homepage` all returned 200 throughout.

**The VG is now fully allocated**, so this trick is spent — growing further
means adding a disk in Proxmox and `pvcreate`/`vgextend` first.

Still worth doing as hygiene, though no longer urgent at 19 GB free: prune old
images on a weekly timer, or as a step at the end of a successful deploy.

```bash
docker image prune -af --filter 'until=168h'   # ~1.2 GB reclaimable today
```

`tadmin` has `NOPASSWD: ALL` on Delta, so this runs over SSH with key auth.

### 6.3 CephFS is mounted with full cluster-admin credentials on the box that runs CI

**What is set up.** `/mnt/cephfs` is mounted from `/etc/fstab` using the
**`client.admin`** CephX identity — the Ceph cluster's root-equivalent account:

```
admin@<fsid>.cephfs-pve3=/ /mnt/cephfs ceph _netdev,nofail,noatime,
  mon_addr=...,secretfile=/etc/ceph/admin.keyring 0 0
```

The secret is at `/etc/ceph/admin.keyring`, mode `0600 root:root`. The same host
also runs `actions.runner.DrexelTriangle-triangle-cms.thetriangle-delta.service`
as user `triangle-runner`.

**Why this matters.** `client.admin` is not scoped to our media directory — it
grants administrative control of the **entire Proxmox Ceph cluster**: every
pool, every other VM's disks, all 7.1 TB. The media tree we actually use is one
subdirectory of it.

The runner cannot read the keyring directly (it is `0600 root`) and its sudo is
limited to `nginx -t` / `nginx -s reload`. **But `triangle-runner` is in the
`docker` group, and docker group membership is root-equivalent** — any process
running as that user can start a container that bind-mounts `/` and read the
keyring as root. So the practical chain is:

> code running in a GitHub Actions job → `triangle-runner` → `docker` group →
> root on Delta → `/etc/ceph/admin.keyring` → **admin of the whole Ceph cluster**

The first link is ordinary CI. Any workflow change, compromised dependency, or
misconfigured `pull_request` trigger that executes attacker-controlled code
reaches cluster admin. Nothing here is currently known to be compromised — this
is about blast radius being far larger than the job requires.

**Suggested fix.** Ask the Proxmox administrator for a CephX identity scoped to
the media path, and mount with that instead. Roughly:

```bash
ceph fs authorize cephfs-pve3 client.triangle-media /media rw
```

which yields caps along the lines of `mon 'allow r'`,
`mds 'allow rw path=/media'`, `osd 'allow rw tag cephfs data=cephfs-pve3'`.
Then write that key to `/etc/ceph/triangle-media.keyring` (`0600 root:root`),
change the fstab source from `admin@...` to `triangle-media@...`, point
`secretfile=` at the new file, remount, and **remove
`/etc/ceph/admin.keyring` from Delta entirely**.

After that, the worst case for a compromised runner is read/write to the media
directory it already serves, instead of the whole cluster.

Worth doing alongside: move the runner off the box that holds storage
credentials, or drop `triangle-runner` from the `docker` group and give it a
narrower deploy path.

### 6.4 DB host addresses sit in a DHCP pool

`10.248.40.154` and `10.248.40.183` came from Drexel's dynamic pool; the old
leases lapse around 2026-08-05. Both containers are pinned static at the
Proxmox *and* in-container layers, so they will not lose their addresses — but
nothing stops the DHCP server handing the same address to a different device
later. DHCP is central Drexel (server `10.254.5.41`); Proxmox is not the DHCP
server, so no reservation can be made from our side.

**This risk was reviewed and deliberately accepted** — other production VMs run
on pool addresses and have never collided. Revisit only on unexplained
intermittent DB connection failures; first diagnostic is
`arping -I vmbr0 -c 2 10.248.40.154` from the Proxmox host (expect MAC
`bc:24:11:e5:cc:58`).

---

## 7. Quick reference

```bash
# health
curl -s -o /dev/null -w '%{http_code}\n' https://delta.thetriangle.org/healthz
curl -s -o /dev/null -w '%{http_code}\n' https://delta.thetriangle.org/v1/health/db

# which slot is live
ssh tadmin@10.248.40.168 'sudo cat /etc/nginx/triangle-cms/active-upstreams.conf'

# backend logs
ssh tadmin@10.248.40.168 'docker logs --since 30m triangle-cms-backend-blue-1 2>&1 | grep -iE "error|fatal|panic"'

# database
ssh tadmin@10.248.40.154 'sudo -n mariadb --default-character-set=utf8mb4 triangle'

# manual backup
ssh tadmin@10.248.40.154 'sudo -n mariadb-dump --single-transaction --databases triangle | gzip > ~/triangle-$(date +%Y%m%d).sql.gz'
```
