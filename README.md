# Triangle CMS (Delta)

## Foreword

Triangle CMS (Delta) is the Drexel Triangle's solution for a minimal WordPress replacement. Its core is a headless CMS (Content Management System) that serves and edits content from a local database. WordPress content can be migrated into Delta via the `wordpress-etl` tool. Delta also offers a minimal frontend similar to WordPress, and because the core CMS is headless, users are welcome to build their own. Delta also includes built-in logging, accessible through a Grafana dashboard, to enable easy monitoring of API calls and internal errors.

## Developer Guide

### Project Structure

Triangle CMS is split into:
- `server/`: Go backend
- `frontend/`: React frontend
- `observability/`: Loki + Promtail + Grafana config for logging purposes
- `scripts/`: setup scripts

### API Specification and Data Models

API specification and response/data model documentation live in the project wiki:
- https://github.com/DrexelTriangle/triangle-cms/wiki
  - Endpoints: https://github.com/DrexelTriangle/triangle-cms/wiki/Endpoints
  - Response shapes/models: https://github.com/DrexelTriangle/triangle-cms/wiki/Response-Shapes

### Prerequisites

- Docker and Docker Compose
- Go 1.24+
- Node.js 20+ and npm
- Local clone of `wordpress-etl`: https://github.com/DrexelTriangle/wordpress-etl

### Quick Start

1. Run the WordPress ETL pipeline locally in `wordpress-etl` to generate source SQL files.
2. From `triangle-cms` repo root, generate CMS SQL files:

```bash
./scripts/generate_wordpress_sql.sh
```

By default, the script auto-detects ETL SQL if the `wordpress-etl` repository is in the same directory or the parent directory of `triangle-cms`:
- `../wordpress-etl/logs/sql`
- `./wordpress-etl/logs/sql`

3. Start only MariaDB in Docker (recommended for local backend development):

```bash
docker compose up -d mariadb
```

This starts:
- MariaDB (`mariadb`) on `localhost:${MARIADB_PORT_FORWARD:-3306}` (container port `3306`), populated from the ETL pipeline output

If you want the full Docker stack (CMS + observability), run:

```bash
./scripts/setup-containers.sh
```

This starts:
- MariaDB (`mariadb`) on `localhost:${MARIADB_PORT_FORWARD:-3306}` (container port `3306`), populated from the ETL pipeline output
- CMS backend (`cms`) on `https://localhost:8080` which exposes the API
- Promtail (`promtail`) which collects logs from Docker containers
- Loki (`loki`) on port `3100`, which indexes logs from Promtail
- Grafana (`http://localhost:3000`, default `User:admin, Password:admin`) which allows you to explore and query logs from Loki

Important:
- Logging/observability (Promtail, Loki, Grafana) is only available when running the full Docker stack.
- If you run the backend locally via `go run` (instead of the `cms` Docker container), those Docker logging pipelines do not apply to your local process.

Reset all compose volumes and rebuild:

> [!WARNING]
> Running this command will erase all logs and database entries, equivalent to a fresh install

```bash
./scripts/setup-containers.sh --reset-data
```

### Local Development

#### Backend (Go API)

1. Ensure MariaDB is running (via Docker or local instance).
2. If using Docker MariaDB and running the backend via `go run`, configure `server/.env` with the same values used by your compose `MARIADB_DATABASE`, `MARIADB_USER`, and `MARIADB_PASSWORD`:

```env
DB_NAME=triangle
DB_USER=triangle_user
DB_PASSWORD=triangle_password
DB_HOST=127.0.0.1
DB_PORT=3306
```

If using a separate local MariaDB instance, configure `server/.env` for that instance instead.

3. If the Docker `cms` service is running, stop it first to avoid port conflict on `:8080`:

```bash
docker compose stop cms
```

4. Run backend:

```bash
cd server
go run ./main.go
```

The backend serves HTTPS on `https://localhost:8080` using (the certs are just there to keep Postman happy):
- `server/certs/localhost.crt`
- `server/certs/localhost.key`

#### Apply Backend Changes to Docker CMS

When you change Go backend code under `server/` and want those changes reflected in the Docker `cms` container, rebuild and restart that service image:

```bash
docker compose up -d --build cms
```

If your change is to MariaDB bootstrap SQL files in `server/internal/database/wordpress_etl/`, those are only applied on fresh DB initialization. Recreate volumes for those to take effect:

> [!WARNING]
> Running this command will erase all logs and database entries, equivalent to a fresh install

```bash
./scripts/setup-containers.sh --reset-data
```

#### Frontend (Vite)

```bash
cd frontend
npm install
npm run dev
```

Vite dev server starts on `http://localhost:5173`.

### WordPress SQL ETL Flow

SQL files for bootstrap imports live in:
- `server/internal/database/wordpress_etl/`

Before running this step, you must have already run the ETL pipeline in:
- https://github.com/DrexelTriangle/wordpress-etl

Generate CMS SQL files from the ETL pipeline output:

```bash
./scripts/generate_wordpress_sql.sh [source_sql_dir] [output_dir]
```

Defaults:
- source: auto-detected (`../wordpress-etl`, `../wordpress-etl/logs/sql`, `./wordpress-etl`, `./wordpress-etl/logs/sql`)
- output: default (`server/internal/database/wordpress_etl`)

Overrides:
- Pass explicit args:
  `./scripts/generate_wordpress_sql.sh ../wordpress-etl/logs/sql server/internal/database/wordpress_etl`
- Or use env vars:
  `WP_ETL_SQL_DIR=../wordpress-etl ./scripts/generate_wordpress_sql.sh`
  `WP_ETL_OUT_DIR=server/internal/database/wordpress_etl ./scripts/generate_wordpress_sql.sh`
  `WP_ETL_SQL_DIR=../wordpress-etl WP_ETL_OUT_DIR=server/internal/database/wordpress_etl ./scripts/generate_wordpress_sql.sh`

Generated files:
- `01-authors.sql`
- `02-articles.sql`
- `03-articles-authors.sql`
- `04-seo.sql`

These are mounted into MariaDB init at container startup through `docker-compose.yml`.

### Testing

Backend tests:

```bash
cd server
go test ./...
```

Test coverage:

Run to see the percentage of code each test covers

```bash
cd server
go test -coverprofile=cover.out ./...
```

This also generates the `cover.out` file, showing exactly which lines are run during tests.
