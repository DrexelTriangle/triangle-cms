# Triangle CMS (Delta)

Headless CMS replacement for The Triangle, with:
- `server/` Go API
- `frontend/` React frontend
- `observability/` Loki + Promtail + Grafana
- `scripts/` local setup helpers

API docs and data models: https://github.com/DrexelTriangle/triangle-cms/wiki

## Prerequisites

- Python 3.10+
- Git
- Docker + Docker Compose
- Go 1.24+
- Node.js 20+ + npm

## First-Time Setup (Recommended)

Create a `triangle` directory first, then clone `triangle-cms` into it
(`triangle-cms` contains the setup script):

```bash
mkdir triangle
cd triangle
git clone https://github.com/DrexelTriangle/triangle-cms.git
cd triangle-cms
```

Then run:

```bash
python ./scripts/first_time_setup.py
```

When run from inside `triangle/triangle-cms`, this installs dependencies for the current checkout and clones:
- `wordpress-etl`
- `Scalene`
as sibling directories inside `triangle/`.

If you want a custom location, set `--target-dir` explicitly.

This prepares and installs dependencies for:
- `triangle-cms`
- `wordpress-etl`
- `Scalene`

Optional flags:
- `--pull` to update already-cloned repos (`git pull --ff-only`)
- `--skip-embeddings` to skip `sentence-transformers` install in `wordpress-etl`

## First Local Run

0. Create Docker env vars for required secrets:

```bash
cp .env.example .env
```

Update `.env` with strong values before running Compose.

1. Generate ETL SQL in `wordpress-etl`:

```bash
cd ../wordpress-etl
.venv/bin/python main.py
```

Windows PowerShell:

```powershell
cd ../wordpress-etl
.venv/Scripts/python.exe main.py
```

For embeddings SQL:

```bash
cd ../wordpress-etl
.venv/bin/python main.py --generate-embeddings
```

2. Copy ETL SQL into CMS bootstrap files:

```bash
cd ../triangle-cms
python ./scripts/generate_wordpress_sql.py
```

3. Start services (Path A is recommended):

Path A (recommended): full Docker stack (CMS + observability):

```bash
python ./scripts/setup_containers.py
```

This stack uses a shared Docker bridge network scoped to the Compose project, so services can resolve each other by container/service name (for example `mariadb`, `loki`, `grafana`).

Path B: Docker MariaDB + local Go backend:

```bash
docker compose up -d mariadb
```

Then run backend locally:

```bash
cd server
go run ./main.go
```

4. Verify API (works with either path):

```bash
curl -k https://localhost:8080/v1/articles/christmas
```

5. Run the Triangle CMS frontend dashboard:

```bash
cd frontend
npm run dev -- --port 5173
```

Frontend dashboard: `http://localhost:5173`

6. Run Scalene frontend:

```bash
cd ../Scalene
npm run dev -- --port 4321
```

Scalene: `http://localhost:4321`

## Common Tasks

Reset DB + logs (fresh install):

```bash
python ./scripts/setup_containers.py --reset-data
```

Run backend locally (instead of Docker `cms` service):

```bash
docker compose stop cms
cd server
go run ./main.go
```

If running backend locally against Docker MariaDB, create `server/.env`:

```env
DB_NAME=triangle
DB_USER=triangle_user
DB_PASSWORD=triangle_password
DB_HOST=127.0.0.1
DB_PORT=3306
ACTIVITY_DB_PATH=./data/activity
```

CMS activity events are stored in BadgerDB at `server/data/activity` by default for local runs, and in the `cms_activity_data` Docker volume for the Compose stack.

Run Triangle CMS frontend dashboard:

```bash
cd frontend
npm run dev -- --port 5173
```

Frontend dashboard: `http://localhost:5173`

> [!NOTE]
> If you want Scalene to use your local CMS instead of our production WordPress you need to be on the `CMS-Testing` branch
> 
> Use `git switch CMS-Testing` to switch

Run Scalene:

```bash
cd ../Scalene
npm run dev -- --port 4321
```

Scalene: `http://localhost:4321`

## ETL SQL Script (Advanced)

Default usage:

```bash
python ./scripts/generate_wordpress_sql.py
```

Optional override usage:

```bash
python ./scripts/generate_wordpress_sql.py [source_sql_dir] [output_dir]
```

Environment variable overrides:

```bash
WP_ETL_SQL_DIR=../wordpress-etl/logs/sql WP_ETL_OUT_DIR=server/internal/database/wordpress_etl python ./scripts/generate_wordpress_sql.py
```

Generated files:
- `01-authors.sql`
- `02-articles.sql`
- `03-articles-authors.sql`
- `04-seo.sql`
- `05-article-embeddings.sql` (real file when available, placeholder otherwise)
- `06-taxonomy.sql`
- `07-poll-counts.sql`
- `08-comments.sql` (real file when available, empty table placeholder otherwise)

## API Docs (Swagger)

Swagger UI is available at `https://localhost:8080/swagger/index.html` when the server is running.

The docs are generated automatically during the Docker build — no manual step needed.

If running the server locally (Path B), regenerate the docs after changing any handler annotations:

```bash
cd server
swag init --parseDependency --parseInternal
```

To add or update docs for an endpoint, add swaggo annotations directly above the handler function in `server/internal/handlers/handlers.go`. Example:

```go
// @Summary My endpoint
// @Tags mytag
// @Produce json
// @Success 200 {object} map[string]string
// @Router /v1/my-endpoint [get]
func MyHandler(...) {
```

Tag order in the UI is controlled by the `@tag.name` lines in `main.go`.

## Testing

```bash
cd server
go test ./...
```

Coverage:

```bash
cd server
go test -coverprofile=cover.out ./...
```
