# Triangle CMS Delta deployment

Production deployment shape for Delta, kept separate from local development and
from database/proxy infrastructure.

## Runtime topology

Delta runs host Nginx on port 80, a self-hosted GitHub Actions runner (labels
`drexel-vpn`, `delta`, `triangle-cms`), and four containers:

| Slot | Frontend | Backend |
|---|---|---|
| blue | `127.0.0.1:8091` | `127.0.0.1:8081` |
| green | `127.0.0.1:8092` | `127.0.0.1:8082` |

The CMS Compose project does not run MariaDB, MaxScale, Loki or Promtail. The
backend connects to the external database/proxy endpoint given by `DB_HOST` and
`DB_PORT` in the host-only `cms.env`.

Loki and Promtail do run on Delta, in the separate `triangle-observability`
Compose project, which lives in
[`triangle-infrastructure`](https://github.com/DrexelTriangle/triangle-infrastructure).
Nothing here starts or stops it: the deploy scripts pin `COMPOSE_FILE` to
`compose.cms.yml` so a CMS deploy cannot tear the log stack down, and
`restart: unless-stopped` keeps it up across deploys.

## Observability

Prometheus, Loki, Promtail, Alertmanager, blackbox and the two read-only nginx
endpoints central Grafana queries all **moved to
[`triangle-infrastructure`](https://github.com/DrexelTriangle/triangle-infrastructure)**
and are applied with `ansible-playbook playbooks/observability.yml`. This repo's
workflow no longer deploys them.

Two things about that stack constrain the CMS:

- **Prometheus joins this project's Compose network**, declared `external` on
  its side. The CMS stack must be up before the observability stack will start,
  and **renaming a service in `compose.cms.yml` breaks scraping in a different
  repository**: `observability/prometheus/prometheus.delta.yml` over there
  scrapes both slots by container name.
- **The backend exposes unauthenticated Prometheus metrics on `GET /metrics`.**
  That is safe only because the host nginx proxies just `/v1` and `/swagger`, so
  nothing routes it in from outside and Prometheus reaches it on the slot's
  loopback port. If a vhost ever forwards `/metrics`, put it behind auth first:
  it exposes route names, traffic volumes and error rates.

`up{job="cms"}` returns **two** series, one per slot, labelled `slot="blue"` /
`slot="green"`. The idle slot being up is normal and says nothing about which
one serves traffic.

The local stack in the repo-root `docker-compose.yml` keeps its own Prometheus
(`observability/prometheus/prometheus.dev.yml`). Its Loki, Promtail and Grafana
services were removed when their shared configs moved, so `docker compose logs`
is the local log story.

### Reading blue/green logs in Grafana

Both slots run all the time, only one receives traffic, and **nothing in the log
stream says which one**: the idle slot goes on emitting healthchecks
indefinitely, so it is easy to read the wrong slot's logs and conclude the site
is idle. The active slot is whatever Nginx points at:

```
sudo cat /etc/nginx/triangle-cms/active-upstreams.conf   # set $triangle_cms_slot blue;
```

Filter by slot with the `compose_service` label (`backend-blue`,
`frontend-green`) or `container` (`triangle-cms-backend-blue-1`). Two
consequences of how Compose names things:

- Container names are reused across deploys, so labels do not churn and
  cardinality stays flat. But a slot's pre-deploy and post-deploy containers
  land in the *same* stream, so a deploy boundary is visible only by timestamp.
- A slot's logs are captured from the moment its container starts, including
  startup and crash output, even though discovery only refreshes every 15s.
  Promtail backfills from the beginning of a newly discovered container's log,
  so a container that dies during a deploy still gets its logs shipped.

Nginx serves whichever frontend slot is active and proxies `/v1`, `/swagger` and
`/swagger/` to the matching backend slot. The initial config listens on HTTP
with `server_name _`, so it works through Delta's VPN IP or hostname before a
public domain exists.

When `cms.thetriangle.org` is ready: set that `server_name`, configure HTTPS,
update `FRONTEND_ORIGIN` and `OIDC_REDIRECT_URI` in `cms.env`, and update the
GitHub environment variable `DELTA_PUBLIC_BASE_URL`. The backend stays in
`CMS_SERVER_MODE=internal-http` behind Nginx.

## Files

- `compose.cms.yml` - Delta-only blue/green frontend/backend slots.
- `cms.env.example` - sanitized variable-name-only production env template.
- `nginx/triangle-cms.conf` - **moved** to
  [`triangle-infrastructure`](https://github.com/DrexelTriangle/triangle-infrastructure)
  (`roles/delta_cms_host/`). Install it with `playbooks/delta-host.yml`.
- `nginx/triangle-cms-active-upstreams.conf.example` - generated include seed.
- `scripts/deploy.sh` - deploy exact SHA to inactive slot, switch, smoke test.
- `scripts/rollback.sh` - explicit rollback to the other slot or a named slot.

## One-time server bootstrap

A workflow cannot safely install and register its own runner, so bootstrap is a
manual server task:

1. Install Docker, the Compose plugin, Nginx, curl or wget, and flock.
2. Create a least-privilege local user for deployments.
3. Register a self-hosted runner on Delta inside the Drexel VPN, with labels
   `drexel-vpn`, `delta`, `triangle-cms`.
4. Allow the runner user to run Docker and reload/test Nginx. Prefer narrow
   sudoers rules for `/usr/sbin/nginx -t` and `/usr/sbin/nginx -s reload` only.
5. Place the host-only production env file at `DELTA_CMS_ENV_FILE`. Not in git.
6. Install the host Nginx site with `triangle-infrastructure`'s
   `playbooks/delta-host.yml` (below).
7. Create the runtime-state directory and seed the active upstream include:

   ```bash
   sudo install -d -o triangle-runner -g triangle-runner -m 0750 \
     /etc/nginx/triangle-cms

   sudo install -o triangle-runner -g triangle-runner -m 0644 \
     deploy/nginx/triangle-cms-active-upstreams.conf.example \
     /etc/nginx/triangle-cms/active-upstreams.conf
   ```

8. Validate Nginx and reload it once.
9. Confirm the runner can pull GHCR images and run `docker compose`.

Keep `/etc/nginx` root-owned and non-writable by the runner. Only
`/etc/nginx/triangle-cms` is runtime state, owned `triangle-runner:triangle-runner`
mode `0750`, with the active include at `0644`. The site itself
(`/etc/nginx/sites-available/triangle-cms.conf`) stays root-owned.

### Installing the Nginx site

**This is Ansible's job.** The site and the runner-owned state directory are
`roles/delta_cms_host` in
[`triangle-infrastructure`](https://github.com/DrexelTriangle/triangle-infrastructure):

```bash
ansible-playbook playbooks/delta-host.yml --limit thetriangle-delta --check --diff
ansible-playbook playbooks/delta-host.yml --limit thetriangle-delta
```

The role creates `/etc/nginx/triangle-cms` and **deliberately does not manage
`active-upstreams.conf` inside it**. That file is written by `deploy.sh` on
every release and read back to determine the live slot, so anything that
templates it silently reverts production to the other slot.

Seed the include once, by hand:

```bash
sudo install -o triangle-runner -g triangle-runner -m 0644 \
  triangle-cms-active-upstreams.conf.example \
  /etc/nginx/triangle-cms/active-upstreams.conf

# The stock default site also matches `server_name _` and can win the vhost pick.
sudo rm -f /etc/nginx/sites-enabled/default
sudo nginx -t && sudo systemctl reload nginx
```

Nginx will not start without `active-upstreams.conf`, since the site `include`s
it unconditionally. A passing `nginx -t` *before* the site is enabled validates
only the stock config and proves nothing.

The site sets `client_max_body_size 91m` on `/v1/`, just above the backend's
`MEDIA_MAX_UPLOAD_BYTES` (90 MiB default). Nginx's stock limit is 1m, which
rejects an ordinary phone photo with a 413 before the CMS sees it, so a deploy
that skips re-copying this file leaves uploads broken while the app looks
correctly configured. Raise both together, never one.

90 MiB is sized to the migrated corpus, which holds unresized camera originals
up to ~77 MiB (largest: `2025/07/BZ9A5771.jpg`). The ceiling above it is
Cloudflare's **100 MB** request-body limit on the tunnel fronting Delta; a body
that passes Nginx and the backend but exceeds that dies at the edge with an
error the CMS never sees. Do not raise the pair past ~95 MiB without moving
uploads off the tunnel.

The backend streams uploads to a temp file rather than buffering them (only the
first 8 MiB stays in memory), so a large upload costs container disk, not
memory.

### Media serving

`location /wp-content/` reads the migrated WordPress corpus straight off CephFS.
It depends on no container, runner or database, so it can be brought up before
the rest of the stack exists. `/` and `/v1/` return 502 until a slot is
deployed; that is expected and does not affect media.

Verify the mount and that the Nginx worker can traverse to it:

```bash
mountpoint /mnt/cephfs
sudo -u www-data ls /mnt/cephfs/media/wp-content/uploads >/dev/null && echo ok
```

A failure there is almost always missing execute permission on a path component
(`sudo chmod o+x /mnt/cephfs /mnt/cephfs/media`), not the Nginx config. On
RHEL-family hosts SELinux blocks the read separately: check
`ausearch -m avc -ts recent` and set `httpd_read_user_content`.

Smoke test with a real file, expecting `200` and
`Cache-Control: public, max-age=2592000, immutable`:

```bash
find /mnt/cephfs/media/wp-content/uploads -name '*.jpg' | head -1
curl -I http://localhost/wp-content/uploads/YYYY/MM/name.jpg
```

### Making the media tree writable

Those checks only prove Nginx can *read*. `POST /v1/media` also has to
**write**, and the rsynced corpus arrives owned by whoever ran the rsync
(`tadmin`), mode 755, while the backend container runs as uid **10001**. Nothing
in the read path notices, so uploads fail long after media serving looks
healthy:

```bash
docker exec triangle-cms-backend-blue-1 \
  sh -c 'touch /mnt/cephfs/media/wp-content/uploads/.wtest && echo ok'
```

If that says `Permission denied`, grant the container uid write on upload
directories. CephFS is mounted with `acl`, so this is additive: ownership and
the migrated files are untouched, and Nginx keeps reading as before. A mount
supporting ACLs does not mean the tools are installed; Ubuntu server images
generally lack them:

```bash
sudo apt-get install -y acl        # setfacl is not installed by default
sudo find /mnt/cephfs/media/wp-content/uploads -type d \
  -exec setfacl -m u:10001:rwx -m d:u:10001:rwx {} +
```

The `d:` (default) entry makes each new `YYYY/MM` directory inherit the grant,
so this does not need repeating every month.

**A later chmod silently disables it.** A chmod on an ACL'd directory sets the
**ACL mask** from the mode's group bits, and the mask caps every named entry:
`chmod 755` leaves `user:10001:rwx` printed but `#effective:r-x`. An rsync run
with `--chmod=D755` does this to every directory it touches (see
`docs/HANDOVER.md`; that command now uses `D775`).

Nothing notices at the time. Uploads keep working until the 1st of the next
month, when the handler first has to create a new `YYYY/MM`, and then every
upload 500s. Check the mask, not the entry:

```bash
getfacl -pc /mnt/cephfs/media/wp-content/uploads/2026 | grep -E '10001|mask::'
# want: user:10001:rwx  (no "#effective:") and mask::rwx
setfacl -m m::rwx /mnt/cephfs/media/wp-content/uploads{,/2026}   # repair
```

Only those two levels matter: `uploads/` creates the year, `uploads/YYYY/`
creates the month. `triangle-infrastructure`'s `delta_cms_host` role repairs
both on every playbook run.

Without the `acl` package, setgid does the same job in plain POSIX, at the cost
of changing group ownership rather than adding a grant beside it. `101` is the
container's gid; the setgid bit is what new directories inherit:

```bash
sudo find /mnt/cephfs/media/wp-content/uploads -type d -exec chgrp 101 {} +
sudo find /mnt/cephfs/media/wp-content/uploads -type d -exec chmod 2775 {} +
```

The failure is easy to misread. `MkdirAll` returns nil for a directory that
already exists, and every migrated `YYYY/MM` directory does exist, so a
permission problem surfaces as `failed to store upload` rather than `failed to
create upload directory`. Check the backend log for the underlying `error=`.

### Media library

Serving the files is independent of *listing* them. The CMS media page reads a
`media` table, which starts empty: the rsynced corpus is on disk but unknown to
the database. After the rsync, populate it once from the CMS (Media -> Reindex)
or directly:

```bash
curl -X POST https://localhost/v1/media/index   # admin session required; returns 202
curl https://localhost/v1/media/index           # poll progress
```

It walks `MEDIA_ROOT/wp-content/uploads`, skips WordPress's generated `-WxH`
thumbnails, and inserts a row per original. It is idempotent: already-indexed
files are skipped and any alt text set in the CMS is preserved, so re-run it
after any later out-of-band rsync. Uploads through the CMS index themselves.

**The index runs in the background.** `POST` returns `202` immediately and `GET`
reports `{running, progress:{walked, scanned, added, skipped}, error}`; a second
`POST` while one is in flight returns `409`. The real corpus is ~145k filesystem
entries and the walk takes minutes, while Nginx cuts an idle upstream read at
60s and Cloudflare at ~100s, so a synchronous version could never finish. A run
is capped at two hours so a wedged filesystem cannot leave the job stuck.

Progress counts entries *walked* rather than files indexed: the corpus is mostly
skipped derivatives, so counting indexed files would look frozen for long
stretches.

### The public photo gallery

`/v1/gallery`, which the public `/photo` page reads, serves only images an
editor has marked (Media -> open an image -> "Show on the photo gallery", or the
"Photo gallery" filter to review the set). The library is every file on the
mount, house ads and comic strips and crossword scans included, so an unfiltered
gallery would be a dump of the upload directory rather than the photo desk's
work.

Reindexing never sets the flag. WordPress kept the same selection as the
`include_in_gallery` attachment meta, so seed it once per cutover from the
legacy database:

```bash
python ./scripts/backfill_gallery_flags.py \
    --wp-dsn  'wordpress@tcp(10.248.42.122)/wordpress' \
    --cms-dsn 'triangle_user@tcp(10.248.40.154)/triangle' --dry-run
```

Passwords come from `WP_DB_PASSWORD` / `CMS_DB_PASSWORD` or a prompt. Drop
`--dry-run` to apply. By default the CMS ends up matching WordPress exactly,
which also *clears* marks made in the CMS since the last run; once editors are
curating in the CMS, use `--additive`. Run it after the media reindex: it
matches on file path, and images the library has not seen are reported and
skipped.

### Disk

Blue/green keeps two frontend and two backend images resident, plus unreaped
prior tags. Prune before Delta's root filesystem fills:

```bash
docker image prune -af --filter 'until=168h'
```

## Required host environment

Copy `cms.env.example` to the private host env path and fill in real values. It
must carry the exact immutable image tag for the active deployment.

`CMS_IMAGE_TAG`, `CMS_BACKEND_IMAGE`, `CMS_FRONTEND_IMAGE`, `DB_NAME`,
`DB_USER`, `DB_PASSWORD`, `DB_HOST`, `DB_PORT`, `OIDC_ISSUER_URL`,
`OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `FRONTEND_ORIGIN`, `OIDC_REDIRECT_URI`,
`CMS_SESSION_TTL_SECONDS`, `CMS_REBUILD_TAXONOMY_COUNTS_ON_STARTUP`, plus:

- `AKISMET_API_KEY` - optional; empty disables comment spam filtering.
- `AKISMET_BLOG_URL` - public site URL Akismet associates with comment checks.
  Required when `AKISMET_API_KEY` is set.
- `MEDIA_HOST_PATH` - host path to the CephFS media tree, bind-mounted into the
  backend. Defaults to `/mnt/cephfs/media`.
- `MEDIA_ROOT` - the same tree as seen *inside* the container. Leave at
  `/mnt/cephfs/media` unless the bind-mount target changes.
- `MEDIA_BASE_URL` - public origin serving `/wp-content/`, used to build media
  URLs returned by the upload endpoint. Empty yields relative URLs.
- `MEDIA_MAX_UPLOAD_BYTES` - per-file upload cap in bytes; empty uses the 90 MiB
  default. Must stay at or below Nginx's `client_max_body_size`.

Keep `CMS_REBUILD_TAXONOMY_COUNTS_ON_STARTUP=false` in production; rebuild
through the admin endpoint after deploys when needed.

New users are created as editors. The first user to log in to an empty
`cms_users` table is bootstrapped as an admin; promote anyone else from the
users screen.

## Required GitHub environment variables

Set these on the `production` GitHub Environment:

- `DELTA_CMS_ENV_FILE` - absolute path to the host-only `cms.env`.
- `DELTA_NGINX_ACTIVE_INCLUDE` - usually
  `/etc/nginx/triangle-cms/active-upstreams.conf`.
- `DELTA_PUBLIC_BASE_URL` - initial HTTP VPN URL or hostname for smoke tests.

Database passwords, OIDC secrets, runner registration tokens, certificates and
server addresses must not be exposed to pull-request workflows. The deploy
workflow runs only on the labelled self-hosted runner and uses the host env
file.

## Deployment

Images are immutable and tagged only with the full commit SHA:

- `ghcr.io/drexeltriangle/triangle-cms-backend:<sha>`
- `ghcr.io/drexeltriangle/triangle-cms-frontend:<sha>`

Automatic publish runs only after a successful CI workflow for a trusted push to
`main`, and tags both images with that exact SHA. Manual publish is
intentionally unsupported.

The deploy workflow checks out deployment code from the protected default
branch. The image SHA is data only: never an Actions checkout ref, script path,
Compose-file source, env-file source, or executable source.

```bash
deploy/scripts/deploy.sh <full-commit-sha>
```

The script acquires an exclusive `flock`, runs preflight checks, reads the
active slot from the Nginx include, pulls the exact SHA images, starts only the
inactive slot, waits for backend `/v1/health/db` and frontend `/healthz`, writes
the active include atomically, runs `nginx -t` and a graceful reload, then runs
public smoke tests through Nginx. It switches back automatically if post-switch
smoke tests fail, and keeps the previous slot running for fast rollback. It
never runs `docker compose down -v` and never deletes persistent data.

Preflight fails before pulling images, starting containers or switching Nginx if
the active include directory is missing or not writable, an existing include is
not readable and writable, the active slot is not `blue` or `green`, `cms.env`
is missing or unreadable, or Nginx validation/reload privileges are unavailable.

## Rollback

Switches Nginx back to the previous running slot, optionally by name:

```bash
deploy/scripts/rollback.sh
deploy/scripts/rollback.sh blue
```

To recover an older image SHA, run the deploy workflow manually with that SHA;
its backend and frontend images must already exist in GHCR. That starts the
inactive slot on those immutable images and switches after health checks.

## Stateful and rollback notes

The backend runs additive, idempotent startup schema operations (`CREATE TABLE
IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS`, one guarded SEO backfill). Rollback
is safe only while database changes stay backward-compatible. Do not deploy
destructive migrations without a backup and a tested restore.

Activity and audit state is in MariaDB. Article edit leases and IP rate-limit
counters live in process memory and can reset during a release switch; shared
locking and rate limiting should move to MariaDB or Redis eventually.
