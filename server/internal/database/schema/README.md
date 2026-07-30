# Canonical table definitions

Every `.sql` file here is the **single source of truth** for one CMS-owned
table. Two consumers read these exact files, so there is no second copy to
drift:

1. **The CMS at startup** — embedded via `go:embed` in
   [../schema.go](../schema.go) and executed by the matching `Ensure*Table`
   function in this package.
2. **The local seed generator** — `scripts/generate_wordpress_sql.py` reads them
   when it writes `wordpress_etl/*.sql`, so a freshly seeded dev database gets
   byte-identical DDL to what production converges to.

Before this existed, the definitions were duplicated between Go string literals
and Python constants, and they had already drifted: the seeded `site_taxonomy`
had a *signed* `id`, no `ENGINE`/`CHARSET`, and was missing
`idx_site_taxonomy_kind` and `idx_site_taxonomy_parent_slug`.

## Rules

- One `CREATE TABLE IF NOT EXISTS` per file, named after the table. `IF NOT
  EXISTS` matters: startup runs these against live databases that already have
  the table.
- **Expand-only.** `CREATE TABLE` alone never alters an existing table, so a new
  column here reaches an existing database only through the `ADD COLUMN IF NOT
  EXISTS` block in the corresponding `Ensure*` function. Add it in both places,
  and never drop or retype a column a rolled-back binary still reads.
- Tables owned by the WordPress ETL (`articles`, `authors`, `seo`, …) are **not**
  here — the ETL emits their DDL. This directory is only for tables the CMS
  creates itself.
