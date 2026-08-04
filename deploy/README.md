# Triangle CMS Delta Deployment

This directory describes the production deployment shape for Delta. It is split
from local development and from database/proxy infrastructure on purpose.

## Runtime Topology

Delta runs:

- Host Nginx on HTTP port 80.
- Self-hosted GitHub Actions runner with labels `drexel-vpn`, `delta`, and
  `triangle-cms`.
- Blue frontend container on `127.0.0.1:8091`.
- Blue backend container on `127.0.0.1:8081`.
- Green frontend container on `127.0.0.1:8092`.
- Green backend container on `127.0.0.1:8082`.

Delta does not run MariaDB, MaxScale, Grafana, Loki, or Promtail in the CMS
deployment Compose project. The backend connects to the external database/proxy
endpoint supplied by `DB_HOST` and `DB_PORT` in the host-only `cms.env`.

Loki and Promtail do run on Delta, but in the separate `triangle-observability`
Compose project (`compose.observability.yml`). Nothing in CI/CD starts it: the
deploy scripts pin `COMPOSE_FILE` to `compose.cms.yml` so that a CMS deploy can
never tear the log stack down. It is brought up by hand, once, and `restart:
unless-stopped` keeps it up across deploys.

## Exposing Loki to the Triangle Grafana

The Triangle Grafana queries Delta's Loki as a datasource. Loki has no
authentication of its own in any configuration, so it is published only to
`127.0.0.1:13100` and fronted by the `nginx/triangle-loki.conf` site on port
3100, which adds basic auth, blocks the write path, and 404s everything that is
not a query.

1. Install the stack into a stable directory on Delta. Do **not** run it out of
   the runner's checkout at
   `/home/triangle-runner/actions-runner/_work/triangle-cms/triangle-cms`: that
   is the only checkout on the host that contains `observability/`, but
   `actions/checkout` resets it on every deploy, which would yank the bind-mount
   sources out from under a long-lived stack. Copy the files to a path the
   runner never touches:

   ```
   mkdir -p ~/triangle-observability
   cd ~/triangle-observability
   sudo cp -r /home/triangle-runner/actions-runner/_work/triangle-cms/triangle-cms/observability .
   sudo cp /home/triangle-runner/actions-runner/_work/triangle-cms/triangle-cms/deploy/compose.observability.yml .
   sudo chown -R "$USER:$USER" observability compose.observability.yml
   ```

   The Compose file resolves its mounts as `../observability/`, so move it into
   a `deploy/` subdirectory alongside the copied tree, or adjust the paths to
   match wherever you put it. Then:

   ```
   docker compose -f compose.observability.yml --env-file ~/triangle-deploy/cms.env up -d
   docker compose -f compose.observability.yml ps
   ```

   `cms.env` must define `GRAFANA_ADMIN_USER` and `GRAFANA_ADMIN_PASSWORD` or
   the stack refuses to start. Re-copy `observability/` after any change to
   those configs lands on main — this copy does not update itself.

2. Create the datasource credentials and install the Nginx site:

   ```
   sudo htpasswd -B -c /etc/nginx/triangle-loki.htpasswd triangle-grafana
   sudo chown root:www-data /etc/nginx/triangle-loki.htpasswd
   sudo chmod 0640 /etc/nginx/triangle-loki.htpasswd
   sudo cp nginx/triangle-loki.conf /etc/nginx/sites-available/
   sudo ln -s ../sites-available/triangle-loki.conf /etc/nginx/sites-enabled/
   sudo nginx -t && sudo nginx -s reload
   ```

   Note that `htpasswd` is not installed on Delta by default; it ships in
   `apache2-utils` on Debian/Ubuntu and `httpd-tools` on RHEL-family hosts.

3. Verify from Delta before handing the details over. Expect `401` then `200`:

   ```
   curl -s -o /dev/null -w '%{http_code}\n' localhost:3100/loki/api/v1/labels
   curl -s -o /dev/null -w '%{http_code}\n' -u triangle-grafana \
     localhost:3100/loki/api/v1/labels
   ```

   `curl -s localhost:3100/ready` reports `Pattern Ingester not ready: waiting
   for 15s after being ready` indefinitely, and that is expected rather than a
   fault: `loki-config.yml` enables `pattern_ingester`, whose readiness never
   latches in a single-binary deployment. Loki serves queries normally. Use the
   authenticated `/loki/api/v1/labels` call above as the real health signal.

   Then confirm logs are actually arriving, which is the check that catches a
   log-driver mismatch. This must return a non-empty list of container names:

   ```
   curl -s -u triangle-grafana \
     'localhost:3100/loki/api/v1/label/container/values'
   ```

4. In the Triangle Grafana, add a Loki datasource with URL
   `http://<delta-vpn-ip>:3100`, Basic auth enabled, and those credentials. No
   path prefix or extra headers are needed.

Delta's own Grafana (`127.0.0.1:3000`, admin credentials from `cms.env`) stays
as a local fallback and is reachable only over an SSH tunnel. Basic auth here
travels in cleartext; fold this endpoint into TLS when TLS lands on Delta.

### Reading blue/green logs in Grafana

Both slots run all the time. Only one receives traffic, and **nothing in the log
stream says which one** -- the idle slot goes on emitting healthchecks
indefinitely, so it is entirely possible to read the wrong slot's logs and
conclude the site is idle. The active slot is whatever Nginx currently points
at:

```
sudo cat /etc/nginx/triangle-cms/active-upstreams.conf   # set $triangle_cms_slot blue;
```

Filter by slot in Grafana with the `compose_service` label
(`backend-blue`, `frontend-green`, ...) or `container`
(`triangle-cms-backend-blue-1`). Two consequences of how Compose names things:

- Container names are reused across deploys, so labels do not churn and
  cardinality stays flat. But the pre-deploy and post-deploy containers for a
  slot land in the *same* stream, so a deploy boundary is only visible by
  timestamp, not by label.
- A slot's logs are captured from the moment its container starts, including
  startup and crash output, even though discovery only refreshes every 15s.
  Promtail backfills from the beginning of a newly discovered container's log,
  so a container that dies during a deploy still gets its logs shipped.

Nginx serves whichever frontend slot is active and proxies `/v1`, `/swagger`,
and `/swagger/` to the matching backend slot. The initial Nginx config listens on
HTTP with `server_name _`, so it works through Delta's VPN IP or hostname before
a public domain exists.

Later, when `cms.thetriangle.org` is ready, update the host Nginx site with that
`server_name`, configure HTTPS certificates, update `FRONTEND_ORIGIN` and
`OIDC_REDIRECT_URI` in `cms.env`, and update the GitHub environment variable
`DELTA_PUBLIC_BASE_URL`. The backend should remain in `CMS_SERVER_MODE=internal-http`
behind Nginx.

## Files

- `compose.cms.yml` - Delta-only blue/green frontend/backend slots.
- `cms.env.example` - sanitized variable-name-only production env template.
- `nginx/triangle-cms.conf` - host Nginx site template.
- `nginx/triangle-cms-active-upstreams.conf.example` - generated include seed.
- `nginx/triangle-loki.conf` - read-only Loki endpoint for the Triangle Grafana.
- `scripts/deploy.sh` - deploy exact SHA to inactive slot, switch, smoke test.
- `scripts/rollback.sh` - explicit rollback to the other slot or named slot.

## One-Time Server Bootstrap

A GitHub workflow cannot safely install and register its own runner. Bootstrap is
a manual server task:

1. Install Docker, the Docker Compose plugin, Nginx, curl or wget, and flock.
2. Create a least-privilege local user for deployments.
3. Register a self-hosted GitHub Actions runner on Delta inside the Drexel VPN.
   Apply the labels `drexel-vpn`, `delta`, and `triangle-cms`.
4. Allow the runner user to run Docker and reload/test Nginx. Prefer narrow
   sudoers rules only for `/usr/sbin/nginx -t` and
   `/usr/sbin/nginx -s reload`.
5. Place the host-only production env file at the path configured by
   `DELTA_CMS_ENV_FILE`. Do not put it in git.
6. Install `nginx/triangle-cms.conf` as an enabled Nginx site.
7. Create the narrow runtime-state directory and seed the active upstream
   include:

   ```bash
   sudo install -d \
     -o triangle-runner \
     -g triangle-runner \
     -m 0750 \
     /etc/nginx/triangle-cms

   sudo install \
     -o triangle-runner \
     -g triangle-runner \
     -m 0644 \
     deploy/nginx/triangle-cms-active-upstreams.conf.example \
     /etc/nginx/triangle-cms/active-upstreams.conf
   ```

8. Validate Nginx and reload it once during bootstrap.
9. Confirm the runner can pull GHCR images and run `docker compose`.

Keep `/etc/nginx` root-owned and non-writable by the runner. Only
`/etc/nginx/triangle-cms` is writable runtime state for deployments, scoped to
the generated active upstream include. The directory should be owned by
`triangle-runner:triangle-runner` with mode `0750`; the active include should be
owned by `triangle-runner:triangle-runner` with mode `0644`. The host Nginx site
such as `/etc/nginx/sites-available/triangle-cms.conf` remains root-owned.

### Installing the Nginx site

Steps 6-8 above, concretely. The repo is not checked out on Delta, so copy the
two files over first (from a workstation, at the repo root):

```bash
scp deploy/nginx/triangle-cms.conf \
    deploy/nginx/triangle-cms-active-upstreams.conf.example \
    <user>@<delta>:/tmp/
```

Then on Delta:

```bash
sudo cp /tmp/triangle-cms.conf /etc/nginx/sites-available/triangle-cms.conf
sudo ln -sf /etc/nginx/sites-available/triangle-cms.conf /etc/nginx/sites-enabled/

sudo install -d -o triangle-runner -g triangle-runner -m 0750 /etc/nginx/triangle-cms
sudo install -o triangle-runner -g triangle-runner -m 0644 \
  /tmp/triangle-cms-active-upstreams.conf.example \
  /etc/nginx/triangle-cms/active-upstreams.conf

# The stock default site also matches `server_name _` and can win the vhost pick.
sudo rm -f /etc/nginx/sites-enabled/default

sudo nginx -t && sudo systemctl reload nginx
```

Nginx will not start without `active-upstreams.conf`, since the site `include`s
it unconditionally. A passing `nginx -t` *before* the site is enabled only
validates the stock config and proves nothing.

The site sets `client_max_body_size 91m` on `/v1/` to sit just above the
backend's `MEDIA_MAX_UPLOAD_BYTES` (90 MiB default). Nginx's stock limit is 1m,
which rejects an ordinary phone photo with a 413 before the CMS ever sees it, so
a deploy that skips re-copying this file leaves media uploads broken while the
app looks correctly configured. Raise both together, never just one.

The 90 MiB figure is sized to the migrated corpus, which contains unresized
camera originals up to ~77 MiB (largest: `2025/07/BZ9A5771.jpg`). The hard
ceiling above it is Cloudflare's **100 MB** request-body limit on the tunnel
fronting Delta; a body that passes Nginx and the backend but exceeds that dies
at the edge with an error the CMS never sees, so do not raise the pair past
~95 MiB without moving media uploads off the tunnel.

The backend streams uploads to a temp file rather than buffering them in RAM
(only the first 8 MiB stays in memory), so a large upload costs container disk,
not memory.

### Media serving

`location /wp-content/` reads the migrated WordPress corpus straight off CephFS.
It has no dependency on the containers, the runner, or the database, so it can be
brought up on its own before the rest of the stack exists. `/` and `/v1/` return
502 until a slot is deployed; that is expected and does not affect media.

Verify the mount and that the Nginx worker user can traverse to it:

```bash
mountpoint /mnt/cephfs
sudo -u www-data ls /mnt/cephfs/media/wp-content/uploads >/dev/null && echo ok
```

A failure there is almost always missing execute permission on a path component
(`sudo chmod o+x /mnt/cephfs /mnt/cephfs/media`), not the Nginx config. On
RHEL-family hosts SELinux blocks the read separately; check `ausearch -m avc -ts
recent` and set `httpd_read_user_content`.

Smoke test with a real file:

```bash
find /mnt/cephfs/media/wp-content/uploads -name '*.jpg' | head -1
curl -I http://localhost/wp-content/uploads/YYYY/MM/name.jpg
```

Expect `200` with `Cache-Control: public, max-age=2592000, immutable`.

### Making the media tree writable (uploads)

The checks above only prove Nginx can *read*. `POST /v1/media` also has to
**write**, and the rsynced corpus arrives owned by whoever ran the rsync
(`tadmin`), mode 755, while the backend container runs as uid **10001**. Nothing
in the read path notices, so uploads fail long after media serving looks healthy:

```bash
docker exec triangle-cms-backend-blue-1 \
  sh -c 'touch /mnt/cephfs/media/wp-content/uploads/.wtest && echo ok'
```

If that says `Permission denied`, grant the container uid write on upload
directories. CephFS is mounted with `acl`, so this is additive -- ownership and
the migrated files are untouched, and Nginx keeps reading as before. The mount
supporting ACLs does not mean the tools are installed; Ubuntu server images
generally lack them:

```bash
sudo apt-get install -y acl        # setfacl is not installed by default
sudo find /mnt/cephfs/media/wp-content/uploads -type d \
  -exec setfacl -m u:10001:rwx -m d:u:10001:rwx {} +
```

The `d:` (default) entry is what makes each new `YYYY/MM` directory inherit the
grant, so this does not need repeating every month.

Without the `acl` package, setgid does the same job in plain POSIX, at the cost
of changing group ownership rather than adding a grant beside it:

```bash
sudo find /mnt/cephfs/media/wp-content/uploads -type d -exec chgrp 101 {} +
sudo find /mnt/cephfs/media/wp-content/uploads -type d -exec chmod 2775 {} +
```

`101` is the container's gid; the setgid bit is what new directories inherit.

The failure is easy to misread. `MkdirAll` returns nil for a directory that
already exists, and every migrated `YYYY/MM` directory does exist, so a
permission problem surfaces as `failed to store upload` rather than `failed to
create upload directory`. Check the backend log for the underlying `error=`.

### Media library

Serving the files is independent of *listing* them. The CMS media page reads a
`media` table, which starts empty: the rsynced corpus is on disk but unknown to
the database. After the media rsync completes, populate it once from the CMS
(Media -> Reindex) or directly:

```bash
curl -X POST https://localhost/v1/media/index   # admin session required; returns 202
curl https://localhost/v1/media/index           # poll progress
```

It walks `MEDIA_ROOT/wp-content/uploads`, skips WordPress's generated `-WxH`
thumbnails, and inserts a row per original. It is idempotent and safe to re-run —
already-indexed files are skipped and any alt text set in the CMS is preserved —
so re-run it after any later out-of-band rsync. Uploads through the CMS index
themselves and need no reindex.

**The index runs in the background.** `POST` returns `202` immediately and `GET`
reports `{running, progress:{walked, scanned, added, skipped}, error}`; a second
`POST` while one is in flight returns `409`. This is not cosmetic: the real corpus
is ~145k filesystem entries and the walk takes minutes, while Nginx cuts an idle
upstream read at 60s and Cloudflare at ~100s. A synchronous version was cancelled
by those proxies every time and could never finish. A run is capped at two hours
so a wedged filesystem cannot leave the job stuck "running" forever.

Progress is reported per entry *walked* rather than per file indexed, because the
corpus is mostly derivatives that are skipped without a stat — counting only
indexed files would look frozen for long stretches.

### The public photo gallery

`/v1/gallery`, which the public site's `/photo` page reads, serves only images an
editor has marked (Media -> open an image -> "Show on the photo gallery", or the
"Photo gallery" filter to review the current set). The library itself is every
file on the mount — house ads, comic strips, crossword scans — so an unfiltered
gallery is a dump of the upload directory rather than the photo desk's work.

Reindexing never sets the flag. WordPress kept the same selection as the
`include_in_gallery` attachment meta, so seed it once per cutover from the legacy
database:

```bash
python ./scripts/backfill_gallery_flags.py \
    --wp-dsn  'wordpress@tcp(10.248.42.122)/wordpress' \
    --cms-dsn 'triangle_user@tcp(10.248.40.154)/triangle' --dry-run
```

Passwords come from `WP_DB_PASSWORD` / `CMS_DB_PASSWORD` or a prompt. Drop
`--dry-run` to apply. By default the CMS ends up matching WordPress exactly,
which also *clears* marks made in the CMS since the last run; once editors are
curating in the CMS, use `--additive` so it only ever adds. Run it after the
media reindex — it matches on file path, and images the library has not seen yet
are reported and skipped.

### Disk

Blue/green keeps two frontend and two backend images resident, plus whatever
prior tags have not been reaped. Delta's root filesystem is small (15 GB), so
prune before it fills:

```bash
docker image prune -af --filter 'until=168h'
```

## Required Host Environment

Copy `cms.env.example` to the private host env path and fill it with real values.
The file must contain the exact immutable image tag for the active deployment:

- `CMS_IMAGE_TAG`
- `CMS_BACKEND_IMAGE`
- `CMS_FRONTEND_IMAGE`
- `DB_NAME`
- `DB_USER`
- `DB_PASSWORD`
- `DB_HOST`
- `DB_PORT`
- `OIDC_ISSUER_URL`
- `OIDC_CLIENT_ID`
- `OIDC_CLIENT_SECRET`
- `FRONTEND_ORIGIN`
- `OIDC_REDIRECT_URI`
- `CMS_SESSION_TTL_SECONDS`
- `CMS_REBUILD_TAXONOMY_COUNTS_ON_STARTUP`
- `AKISMET_API_KEY` - optional; leave empty to disable comment spam filtering.
- `AKISMET_BLOG_URL` - full public site URL Akismet should associate with
  comment checks. Required when `AKISMET_API_KEY` is set.
- `MEDIA_HOST_PATH` - host path to the CephFS media tree, bind-mounted into the
  backend. Defaults to `/mnt/cephfs/media`.
- `MEDIA_ROOT` - the same tree as seen *inside* the container. Leave at
  `/mnt/cephfs/media` unless the bind-mount target changes.
- `MEDIA_BASE_URL` - public origin that serves `/wp-content/`, used to build
  media URLs returned by the upload endpoint. Empty yields relative URLs.
- `MEDIA_MAX_UPLOAD_BYTES` - per-file upload cap in bytes. Empty uses the
  90 MiB default. Must stay at or below Nginx's `client_max_body_size`.

Keep `CMS_REBUILD_TAXONOMY_COUNTS_ON_STARTUP=false` in production. Rebuild
taxonomy through the admin endpoint after deploys when needed.

New users are created as editors. The very first user to log in to an empty
`cms_users` table is bootstrapped as an admin; promote anyone else from the
users screen.

## Required GitHub Production Environment Variables

Configure these as GitHub Environment variables for `production`:

- `DELTA_CMS_ENV_FILE` - absolute path to the host-only `cms.env`.
- `DELTA_NGINX_ACTIVE_INCLUDE` - usually `/etc/nginx/triangle-cms/active-upstreams.conf`.
- `DELTA_PUBLIC_BASE_URL` - initial HTTP VPN URL or hostname for smoke tests.

Production database passwords, OIDC secrets, runner registration tokens,
certificates, and server addresses must not be exposed to pull-request workflows.
The deploy workflow runs only on the labelled self-hosted runner and uses the
host env file.

## Deployment

Images are immutable and tagged only with the full commit SHA:

- `ghcr.io/drexeltriangle/triangle-cms-backend:<sha>`
- `ghcr.io/drexeltriangle/triangle-cms-frontend:<sha>`

Automatic publish runs only after a successful CI workflow for a trusted push to
`main`. It publishes backend and frontend images tagged with that exact commit
SHA. Manual publish is intentionally unsupported.

The deploy workflow checks out trusted deployment code from the protected default
branch. The image SHA is data only: it is never used as an Actions checkout ref,
script path, Compose-file source, env-file source, or executable source.

The deploy workflow runs:

```bash
deploy/scripts/deploy.sh <full-commit-sha>
```

The script:

- Acquires an exclusive `flock`.
- Runs deployment preflight checks before pulling images or starting containers.
- Reads the active slot from the Nginx include.
- Pulls the exact frontend/backend SHA images.
- Starts only the inactive frontend/backend services.
- Waits for backend `/v1/health/db` and frontend `/healthz`.
- Writes the active Nginx include atomically.
- Runs `nginx -t` and gracefully reloads Nginx.
- Runs public smoke tests through Nginx.
- Switches back automatically if post-switch smoke tests fail.
- Keeps the previous slot running for fast rollback.

It never runs `docker compose down -v` and never deletes persistent data.

The deployment preflight fails before pulling images, starting containers, or
switching Nginx if the active include directory is missing or not writable, an
existing active include is not readable and writable, the active slot is not
`blue` or `green`, `cms.env` is missing or unreadable, or Nginx validation/reload
privileges are not available.

## Rollback

Rollback switches Nginx back to the previous running slot:

```bash
deploy/scripts/rollback.sh
```

You can also name a target slot:

```bash
deploy/scripts/rollback.sh blue
deploy/scripts/rollback.sh green
```

For recovery to an older image SHA, manually run the deploy workflow with that
full SHA. The backend and frontend images for that SHA must already exist in
GHCR. Manual deployment starts the inactive slot with those immutable images and
then switches traffic after health checks.

## Stateful and Rollback Notes

The backend runs additive, idempotent startup schema operations such as
`CREATE TABLE IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS`, and one guarded SEO
backfill. Rollback is safe only while database changes stay backward-compatible.
Do not deploy destructive migrations without a backup and a tested restoration
plan.

Activity/audit state is in MariaDB. Article edit leases and IP rate-limit
counters remain in process memory; they can reset during a release switch. Shared
locking/rate limiting should move to MariaDB or Redis later, but that is outside
this CI/CD implementation.
