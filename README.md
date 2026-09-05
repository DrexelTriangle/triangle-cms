# Triangle CMS (Delta)

Headless CMS replacement for The Triangle.

- `server/` Go API
- `frontend/` React dashboard
- `scripts/` setup and data helpers

The observability stack (Prometheus, Loki, Promtail, Alertmanager, blackbox,
Grafana dashboards) lives in
[`triangle-infrastructure`](https://github.com/DrexelTriangle/triangle-infrastructure).
The backend still exposes `GET /metrics`; scrape it directly in local dev.

API reference and data models: https://github.com/DrexelTriangle/triangle-cms/wiki

## Prerequisites

Python 3.10+, Go 1.24+, Node.js 20+, Docker with Compose, Git.

## First-time setup

Clone into a `triangle/` parent directory; the setup script clones
`wordpress-etl` and `Scalene` as siblings and installs dependencies for all
three.

```bash
mkdir triangle && cd triangle
git clone https://github.com/DrexelTriangle/triangle-cms.git
cd triangle-cms
python ./scripts/first_time_setup.py
```

Flags: `--target-dir` for a custom location, `--pull` to update already-cloned
repos, `--skip-embeddings` to skip `sentence-transformers` in `wordpress-etl`.

## First local run

1. Create `.env` and set strong values:

   ```bash
   cp .env.example .env
   ```

2. Generate ETL SQL (add `--generate-embeddings` for semantic search):

   ```bash
   cd ../wordpress-etl && .venv/bin/python main.py
   ```

   On Windows, `.venv/Scripts/python.exe main.py`.

3. Copy it into the CMS bootstrap files:

   ```bash
   cd ../triangle-cms && python ./scripts/generate_wordpress_sql.py
   ```

4. Start the stack. Path A runs everything in Docker:

   ```bash
   python ./scripts/setup_containers.py
   ```

   Path B runs MariaDB in Docker and the backend locally:

   ```bash
   docker compose up -d mariadb
   cd server && go run ./main.go
   ```

   Compose services resolve each other by service name (`mariadb`). The dev
   stack no longer runs Loki, Promtail or Grafana; their configs moved to
   `triangle-infrastructure`. Use `docker compose logs`.

5. Verify, then start the frontends:

   ```bash
   curl -k https://localhost:8080/v1/articles/christmas
   cd frontend && npm run dev -- --port 5173   # dashboard on :5173
   cd ../Scalene && npm run dev -- --port 4321 # public site on :4321
   ```

> [!NOTE]
> Scalene only reads a local CMS on its `CMS-Testing` branch
> (`git switch CMS-Testing`). Otherwise it hits production WordPress.

## Common tasks

Reset the database and logs:

```bash
python ./scripts/setup_containers.py --reset-data
```

Run the backend locally against the Docker database: `docker compose stop cms`,
then create `server/.env`:

```env
DB_NAME=triangle
DB_USER=triangle_user
DB_PASSWORD=triangle_password
DB_HOST=127.0.0.1
DB_PORT=3306
ACTIVITY_DB_PATH=./data/activity
```

Activity events go to BadgerDB at `server/data/activity` locally, and to the
`cms_activity_data` volume under Compose.

## Rebuild the local DB from the ETL

Runs the ETL, regenerates the seed SQL, recreates the database, verifies, and
restarts the CMS:

```bash
python ./scripts/reseed_from_etl.py
python ./scripts/reseed_from_etl.py --skip-etl       # reuse existing ETL output
python ./scripts/reseed_from_etl.py --no-embeddings  # skip the slow model step
python ./scripts/reseed_from_etl.py --yes            # no confirmation prompt
```

**This is destructive.** The seed files are mounted into
`docker-entrypoint-initdb.d`, which MariaDB replays only on first init, so a
real reseed deletes the `mariadb_data` volume. That takes `cms_users`,
`cms_sessions`, `cms_settings`, polls and the media catalogue with it, not just
imported content. The script prints the row counts at stake and makes you type
`reseed`; it refuses to run without a TTY unless `--yes` is passed.

`--no-embeddings` writes a placeholder that **drops** `article_embeddings`, so
related articles on `/v1/articles/{slug}` come back empty until you re-run with
embeddings.

For rebuilding production data from a fresh WordPress export, see
[`docs/ETL-REBUILD.md`](docs/ETL-REBUILD.md).

## ETL SQL generator

```bash
python ./scripts/generate_wordpress_sql.py [source_sql_dir] [output_dir]
WP_ETL_SQL_DIR=... WP_ETL_OUT_DIR=... python ./scripts/generate_wordpress_sql.py
```

Writes `01-authors.sql`, `02-articles.sql`, `03-articles-authors.sql`,
`04-seo.sql`, `05-article-embeddings.sql`, `06-taxonomy.sql`,
`07-poll-counts.sql`, `08-comments.sql`. Embeddings and comments fall back to
placeholders when the ETL did not produce them.

## API docs (Swagger)

The interactive UI is at `https://localhost:8080/swagger/index.html` while the
server runs. A read-only copy is published to
<https://drexeltriangle.github.io/triangle-cms/> from the `server/docs/swagger.json`
committed on `main`, so it is only as current as the last committed `swag init`,
and "Try it out" is disabled there.

The Docker build regenerates the docs. Running locally (Path B), regenerate them
after changing handler annotations:

```bash
cd server && swag init --parseDependency --parseInternal
```

Document an endpoint with swaggo annotations above its handler in
`server/internal/handlers/handlers.go`:

```go
// @Summary My endpoint
// @Tags mytag
// @Produce json
// @Success 200 {object} map[string]string
// @Router /v1/my-endpoint [get]
func MyHandler(...) {
```

Tag order in the UI comes from the `@tag.name` lines in `main.go`.

## Testing

```bash
cd server
go test ./...
go test -coverprofile=cover.out ./...
```
