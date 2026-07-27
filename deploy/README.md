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
- `CMS_AUTO_PROMOTE_ALL_ADMINS`
- `CMS_REBUILD_TAXONOMY_COUNTS_ON_STARTUP`
- `MEDIA_HOST_PATH` - host path to the CephFS media tree, bind-mounted into the
  backend. Defaults to `/mnt/cephfs/media`.
- `MEDIA_ROOT` - the same tree as seen *inside* the container. Leave at
  `/mnt/cephfs/media` unless the bind-mount target changes.
- `MEDIA_BASE_URL` - public origin that serves `/wp-content/`, used to build
  media URLs returned by the upload endpoint. Empty yields relative URLs.

Keep `CMS_AUTO_PROMOTE_ALL_ADMINS=false` and
`CMS_REBUILD_TAXONOMY_COUNTS_ON_STARTUP=false` in production. Rebuild taxonomy
through the admin endpoint after deploys when needed.

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
