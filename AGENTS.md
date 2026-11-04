# AGENTS.md

Headless CMS ("Delta") replacing WordPress for The Triangle (Drexel student newspaper).
Go API + React/TypeScript dashboard + a sentence-transformers embeddings sidecar. Backed by
MariaDB, deployed blue/green on a self-hosted runner. AI agents: read the "Do not" and
"Deploy / prod" sections before touching anything that writes data or the database.

## Layout

- `server/` — Go API (go.mod `server`, go 1.25). `main.go`, `cmd/`, `internal/` (auth, database,
  handlers, routes, middleware, models, slack, akismet, activity, embeddings, metrics, imaging,
  wordangle). Swagger
  docs in `server/docs/`. Canonical DDL in `server/internal/database/schema/*.sql` (go:embedded).
- `frontend/` — React 19 + TypeScript + Vite 7 dashboard (articles, media, taxonomy, polls,
  classifieds, settings). `src/`, Tailwind, Vitest.
- `scripts/` — Python setup/data helpers: `first_time_setup.py`, `generate_wordpress_sql.py`,
  `setup_containers.py`, `reseed_from_etl.py`, `build-wordangle-wordlists.py`, plus backfill/report one-offs.
- `embeddings/` — FastAPI (onnxruntime) embedding sidecar; image tagged by `git rev-parse HEAD:embeddings`.
- `imaging/` — image-processing sidecar (own Docker image, built in CI).
- `deploy/` — prod Delta deploy: `compose.cms.yml`, `nginx/`, `scripts/{deploy,rollback,common}.sh`.
  Doc: `deploy/README.md`.
- `docs/` — `ETL-REBUILD.md`, `HANDOVER.md`, `api/`.
- `db-backups/` — gitignored local MariaDB dumps (contain hashes/sessions; Borg-backed, keep here).
- `.github/workflows/` — `ci.yml`, `publish.yml`, `deploy.yml`, `rollback.yml`, `pages.yml`.
- `postman/` — API collection. `docker-compose.yml` — local dev stack (mariadb, embeddings, imaging, cms).

Sibling repos (not this repo): `Scalene` (public Astro site), `wordpress-etl` (ETL), `triangle-infrastructure`
(Ansible/observability, private).

## Build / run / test / lint

Backend (`server/`):
```bash
go test ./...            # unit + integration (integration skip w/o CMS_TEST_DSN)
go test -race ./...
go vet ./...
go run ./main.go         # local run against dockerized mariadb
swag init --parseDependency --parseInternal   # regenerate Swagger docs; drift fails CI
```

Frontend (`frontend/`):
```bash
npm ci
npm run dev -- --port 5173   # dashboard on :5173, proxies /v1 to https://localhost:8080
npm run build                # tsc -b && vite build
npm run lint                 # eslint
npm test                     # vitest
```

Scripts / local stack:
```bash
python ./scripts/first_time_setup.py
python ./scripts/generate_wordpress_sql.py [etl_sql_dir] [out_dir]
python ./scripts/setup_containers.py
python ./scripts/reseed_from_etl.py [--skip-etl] [--no-embeddings] [--yes]   # destructive, see below
deploy/scripts/deploy_scripts_test.sh                  # deploy-script test suite (runs in CI)
docker compose -f deploy/compose.cms.yml config        # validate Delta compose
```

Integration tests need a real MariaDB: set `CMS_TEST_DSN=<user>:<pw>@tcp(127.0.0.1:3306)/tax_test?parseTime=true&multiStatements=true`
(tests skip silently when the var is unset). User/pw come from `docker exec triangle-cms-mariadb-1 printenv MARIADB_USER/MARIADB_PASSWORD`
(not root). One-time, as root inside the container: `CREATE DATABASE tax_test` + `GRANT ALL ON tax_test.* TO '<user>'@'%'`.
Run `go test ./internal/... -p 1` to serialize packages sharing the DB. CI never sets `CMS_TEST_DSN`, so these
only run locally; run them unsandboxed, because the agent sandbox blocks localhost TCP to the container.

## Conventions

- Go: swaggo annotations live on handlers in `server/internal/handlers/handlers.go`; `@tag.name` order in `main.go`.
  Run `swag init` and commit `server/docs/` — CI regenerates and fails on any diff.
- Adding an `articles` column: add to `schema/*.sql` AND an `ADD COLUMN IF NOT EXISTS` in
  `EnsureArticlesSchema`, plus every column-list touchpoint in `http_models.go`/`handlers.go`
  (order must match `ScanArticle`). Never hand-edit generated seed SQL.
- Adding an `authors` column: put it only in `EnsureAuthorsSchema`, because the ETL owns the table and neither
  `schema/*.sql` nor `generate_wordpress_sql.py` has its DDL. Then update `AuthorColumns`, both author scanners,
  `LoadAuthorsByArticleIDs`, the `GetAuthors` SELECT/GROUP BY, and the hand-built `authors` fixtures in
  `article_patch_integration_test.go` and `article_embeddings_integration_test.go`.
- Never use `RowsAffected`/`LastInsertId` to tell an insert from a duplicate, because the server connects with
  `clientFoundRows=true` (`database.buildDSN`) and an unchanged `ON DUPLICATE KEY` hit reports 1, like an insert; read the
  row back. Tests opening `CMS_TEST_DSN` raw don't see this unless they set `ClientFoundRows` (see `newsletterTestDB`).
- For insert-if-missing, insert first and then `SELECT … FOR UPDATE`, because a locking read of an absent row takes a shared
  gap lock and concurrent inserts then deadlock (Error 1213).
- Frontend: API calls via `useApiFetch` (prefixes `VITE_API_BASE_URL`); overrides `VITE_AUTH_BASE_URL`,
  `VITE_PUBLIC_SITE_URL` (see `src/auth/urls.ts`).
- Keep `test.env: { NODE_ENV: "test" }` in `frontend/vite.config.ts`, because an exported
  `NODE_ENV=production` (shells, hooks) loads React's prod build and every render fails with "React.act is not a function".
- Seed SQL (`server/**/*.sql`) is gitignored except `schema/*.sql`; `.env`, `deploy/*.env` (except `.example`)
  are gitignored. Never commit secrets/credentials.
- CI runs on every PR (see `ci.yml`): go test/race/vet, npm ci/lint/test/build, Swagger-drift check,
  docker image builds, deploy-script tests, Python compile + reseed-guard + seed id=0 checks, compose config validation.

## Deploy / prod

- Deploy = merge to `main` → CI → Publish Images → Deploy Delta (`workflow_run` chain, ~3 min).
- Deploy runs on a self-hosted runner (labels `drexel-vpn`, `delta`, `triangle-cms`) with GitHub
  `production` environment, which **requires reviewers** — a human approval gate; never script around it.
- **An operator gates every cutover and prod step.** Confirm ambiguous scope (e.g. staging-swap vs DROP)
  before acting. Prod DB writes go only via MaxScale (`:4006`) from Delta — no direct 3306 from a workstation.
- Prod topology: host nginx (port 80) + blue/green slots `{backend,frontend} 8081/8082, 8091/8092`;
  active-slot pointer `/etc/nginx/triangle-cms/active-upstreams.conf` (runtime state, never template).
  Deploy code comes from the protected default branch, not the image SHA (trust boundary).
- `~/triangle-deploy` copies on Delta are stale; the live compose comes from the runner checkout.
- Sudo on Delta needs a password and is blocked non-interactively: give Sachin the root commands to run.

## Do not

- Dev container: use `docker start` / `docker stop`, **never `docker compose up`** (recreates with autostart).
  Never edit the tracked `docker-compose.yml` — put local overrides in a `docker-compose.override.yml`
  that stays untracked (add it to `.gitignore` if you create one).
- **Never reseed, drop or wipe any remote server's database** (prod, staging, dev hosts). This is a standing decision. Remote DB changes are additive/in-place only, with operator approval.
- `reseed_from_etl.py` is for the LOCAL docker stack only, and is destructive (deletes the `mariadb_data` volume: users, sessions, settings, polls,
  media catalogue). It refuses without a TTY unless `--yes`. Back up first, report what's destroyed, and
  raise preservation concerns **before** acting. "Destructive" means destructive: do the literal wipe asked for.
- Never print secrets/keys/webhooks/tokens. Verify shape with `grep -qE`; don't echo values.
- Never drop `triangle_user@<Delta>` (MaxScale auths by source IP — dropping it kills all DB routes while
  `/v1/health` stays 200). Never re-slug a section (hardcoded in Scalene + CMS).
- MaxScale `ROW_COUNT()` / `LAST_INSERT_ID()` lie after a write (land on a replica) → verify by re-reading rows.
- Load seed SQL with `--default-character-set=utf8mb4`; grep loader output for `^ERROR`, never pipe through `head`.
- `cms.env` is read only at container creation → recreate (not restart) containers after env edits.

## Pointers

- API reference & data models: the GitHub wiki (link in README.md). Swagger JSON:
  `server/docs/swagger.json` / https://drexeltriangle.github.io/triangle-cms/swagger.json.
- ETL rebuild for prod data: `docs/ETL-REBUILD.md`. Handover notes: `docs/HANDOVER.md`.
- Deploy detail: `deploy/README.md`. Root: `README.md`.
- Operational rules (meant for humans, still authoritative): `~/.hermes/kb/` (`preferences.md`,
  `gotchas.md`, `runbooks.md`, `access.md`, `locations.md`), `[triangle-cms]` tag.