# Destructive ETL Rebuild — Primer

How to rebuild the `triangle` database from scratch out of a fresh WordPress
export. This is the "nuke and repave" path: `DROP DATABASE` → load six
ETL-generated SQL files → restore the things the ETL does not produce.

Last verified end-to-end **2026-08-01** (9780 articles onto DB1).

---

## 1. What this is, and when you actually need it

The ETL (`wordpress-etl`) turns a WordPress WXR export into six SQL files under
`logs/sql/`. Those files each carry their own `CREATE TABLE`, so loading them
means dropping and recreating the database. There is no incremental mode and no
migration tool.

Reach for a full rebuild when:

- article **content** must change wholesale — image URL canonicalization,
  byline fixes, category/tag handling, un-dropping comics, etc. All of these are
  baked into the data at ETL time, so an env-var change alone does nothing.
- you are pulling a newer WordPress export.

Do **not** rebuild for CMS-side schema changes. The Go backend applies those
itself at startup via `EnsureArticlesSchema` (`server/internal/database/users.go`)
with idempotent `ADD COLUMN IF NOT EXISTS`.

## 2. What it destroys

The ETL only owns six tables: `articles`, `authors`, `articles_authors`, `seo`,
`article_embeddings`, `comments`.

Everything else in the database is CMS-side and is **created empty at runtime**
by the Go backend. `DROP DATABASE` therefore wipes real editorial state:

| Lost | Consequence |
| --- | --- |
| `site_taxonomy` | **Every section page on the public site 404s.** Not "counts read 0" — Scalene resolves section routes from `/v1/taxonomy`, and an empty response means `/entertainment` etc. render the 404 page. Sections are config, not test data. |
| `media.in_gallery` | `/photo` goes empty. The gallery is a hand-curated 25-image set; nothing re-seeds it. |
| `cms_users`, `cms_sessions` | Everyone is logged out. Auto-promote recreates the first user on next login. |
| `cms_settings` | Resets to defaults — breaking-news banner text is gone. |
| `cms_activity`, `cms_poll_counts` | Gone. |

Take a dump first, always. DB1 is still the only live copy — there is no DB2.

```
mariadb-dump --single-transaction --routines --events --databases triangle | gzip > pre-reset.sql.gz
```

Pull a copy off-host. You will need this dump again in step 6 to restore
`site_taxonomy`.

## 3. Prerequisites

**The export is two separate WordPress downloads, zipped by hand** into
`wordpress-etl/Data/wp-export.zip` (path hardcoded at `Utils/Constants.py`).
Members are matched by case-sensitive filename prefix:

- `wp-posts*.xml` — posts. An "All content" export is safe here; the extractor
  drops anything that isn't `post_type == "post"`.
- `wp-guestAuths*.xml` — a WXR export of the Co-Authors Plus `guest-author` post
  type. **Do not reuse the all-content export for this half** — the guest-author
  parser does not filter by post type and would run every post through the
  author path.

Also confirm before starting:

- Media is synced to Ceph (see step 7 — do it *after* the load, but know the gap
  exists).
- You know which `MEDIA_BASE_URL` you want baked into the data. It must be a
  public hostname, never a private IP: the ETL writes it into `photo_url` **and**
  into inline `<img>` tags in article bodies, both of which render to browsers.

## 4. Run the ETL

There is a real `--headless` now (ETL commit `2975d05`). No TUI, no scratchpad
driver — the older notes describing a stub driver are obsolete.

```
cd wordpress-etl
MEDIA_BASE_URL=https://delta.thetriangle.org \
  .venv/bin/python main.py --generate-embeddings --best-guess --headless
```

Embeddings take about 65 seconds for ~10k articles. `--headless` requires
`--best-guess`. Note `triangle-cms/scripts/reseed_from_etl.py` does *not* pass
`--headless`, so it still opens the TUI, and it is local-stack only.

Output lands in `wordpress-etl/logs/sql/`:

```
articles.sql  authors.sql  articles_authors.sql  seo.sql  comments.sql  article_embeddings.sql
```

### Prompt traps

`--headless` claims it cannot prompt. That is not quite true. It covers author
*matching*, but duplicate-record **field conflicts** still hard-exit the run.
Resolved conflicts are cached in `logs/auth_conflicts.json`.

Historically both ID-keyed caches (`auth_conflicts.json` and
`logs/article-sanitizer/article_author_resolution_cache.json`) rotted between
exports, because **WordPress renumbers *and reuses* author IDs**. A stale ID
either orphaned the link or silently credited an article to the wrong person.
Fixed upstream in PRs #54/#56/#57: a cached decision now records *which person*
wins, and the ID is always re-looked-up from the current export. If you ever
touch that code again, keep that invariant — never write a cached ID.

## 5. Load into DB1

DB1 is `10.248.40.154`; `tadmin` has NOPASSWD sudo there. Delta cannot ssh to
DB1 (host key unverified, and `triangle_user` is granted only from `.183`/`.168`),
so load from the workstation.

```
ssh tadmin@10.248.40.154 'sudo -n mariadb --default-character-set=utf8mb4 -e "DROP DATABASE triangle; CREATE DATABASE triangle;"'

for f in authors articles articles_authors seo article_embeddings comments
  gzip -c logs/sql/$f.sql | ssh tadmin@10.248.40.154 'gunzip | sudo -n mariadb --default-character-set=utf8mb4 triangle'
end
```

Load in that order — `articles_authors` has FK-shaped dependencies on the first
two.

### `--default-character-set=utf8mb4` is not optional

DB1's `character_set_client`/`character_set_connection` default to **latin1**.
Piping a UTF-8 file into a bare `mariadb` double-encodes every multibyte
character: `“` (`E2 80 9C`) is stored as `â€œ`. This cost a full reload on
2026-08-01.

**The obvious check cannot detect it.** Reading the column back through a bare
client converts it a second time and prints a correct-looking `“` — the two
errors cancel. Verify with `SELECT HEX(...)` and expect `E2809C`. Likewise,
`LIKE "%â€%"` typed at a shell gets mangled in transit and returns 0 rows, which
reads as "no corruption".

### Always grep the loader output for `^ERROR`

A partial load looks like a successful one. The classic failure is the authors
file aborting halfway on a duplicate key while every other table loads fine. Do
not pipe loader output through `head`.

(`article_embeddings.sql` legitimately lacks the `NO_AUTO_VALUE_ON_ZERO`
preamble the other five carry — its PK is a bare `BIGINT`, not `AUTO_INCREMENT`,
so the `id=0` row stores literally. Don't "fix" it.)

## 6. Restore what the ETL doesn't produce

**Order matters: restart the backend first.** `site_taxonomy` is created at
runtime by the Go code, so restoring into a freshly dropped database fails with
`Table 'triangle.site_taxonomy' doesn't exist`.

1. Restart **both** blue and green backends. Green serves; blue is the rollback
   target and must see the new schema too.
2. Restore sections from the pre-reset dump:
   ```
   zcat pre-reset.sql.gz | awk '/^INSERT INTO `site_taxonomy`/,/;$/' > taxonomy.sql
   ```
   Load it the same way as above. Only 7 of the ~35 rows are `kind='section'`,
   but restore all of them.
3. Backfill parent sections. The ETL writes whatever categories WordPress had,
   which for a subsection article is the subsection alone — so the row carries
   "Welcome Week" with no "Special Editions" above it. Section *listing* still
   finds those articles (a section query expands to its subsections), but
   anything reading `articles.categories` directly, including the public site's
   per-article section labels, sees an unparented subsection. Must run after
   step 2: it reads the section tree out of `site_taxonomy`.
   ```
   python ./scripts/backfill_parent_sections.py \
       --cms-dsn 'triangle_user@tcp(10.248.40.154)/triangle' --dry-run
   ```
   Via MaxScale add `--skip-ssl`: the proxy does not offer TLS and MariaDB
   clients 11.4+ require it by default. Delta has no MariaDB client package —
   `docker run --rm -i -v /tmp:/tmp mariadb:11.7 mariadb "$@"` as a shim on
   `PATH` works, and the `-v /tmp:/tmp` matters so the script's 0600 option file
   resolves inside the container.

   Additive and idempotent — it only appends a missing parent, never removes or
   reorders — so re-running is a no-op and `--dry-run` first costs nothing. It
   refuses any row whose categories text opens with `[` but will not parse as
   JSON, reporting the ids rather than risk corrupting them.

   **Read the dry run before applying.** It only adds a parent the article is
   not already matched by, using the same fuzzy substring rule the read path
   uses — so an article tagged `Arts & Entertainment` does *not* also get
   `Entertainment`, because `%entertainment%` already matches it. First run on
   DB1 (2026-08-02) rewrote **638 of 10058** rows: mostly `Comics` → adds
   `Comics & Puzzles` (329), plus `Music`/`Cooking` → `Entertainment`,
   `Public Safety` → `News`, `Men's Basketball` → `Sports`.
4. Restart the backend again to rebuild taxonomy counts. Note this is *not*
   needed on account of step 3: counts bucket by section **plus its
   subsections**, so a subsection-only article was already counted — verified on
   2026-08-02, `comics-puzzles` read 331 both before and after. The backfill is
   count-neutral; it changes which sections an article *names*, not the totals.
5. Re-flag the photo gallery — 25 rows in `media.in_gallery`. Source of truth is
   `https://cms.thetriangle.org/wp-json/triangle/v1/gallery` (disappears when
   WordPress is retired); `scripts/backfill_gallery_flags.py` is the durable
   route but has never been run against the real WP DB.
6. Log back in; re-enter any breaking-news text.

### Never re-slug a section to fix a count

Section slugs are hardcoded in **both** repos (Scalene's `Header.astro`,
`SideMenu.astro`, `index.astro`, `siteConfig.ts`; the CMS's `handlers.go`
homepage block). Changing `comics-puzzles` → `comics` on 2026-08-01 returned
400s on the live site. The correct fix for an under-matching section is to add a
**subsection** whose slug matches the real category and leave the section slug
alone.

## 7. Post-load verification

Run all of these. Each has produced a real defect at least once.

**Media gap.** A fresh export references images added to WP after the last
rsync. Test the filesystem, not curl — Cloudflare caches 404s:

```
while read p; do [ -f "/mnt/cephfs/media$p" ] || echo "$p"; done
```

On 2026-08-01, 24 of 6323 featured images were absent, and 22 of those are
absent on WordPress itself (irreducible). Sync must be **pushed** from the WP VM
`10.248.40.141`; Delta cannot pull. rsync exiting **23** with "failed to set
times on .../2026/08" is one directory's mtime, not a data failure — ignore it.

Newly synced files can 404 publicly for up to 4 hours while the pre-sync edge
404 ages out. `?v=<ts>` returns 200 immediately; that confirms the file is fine.

**Orphaned author links** — byline present in `articles.authors` but the join
returns nothing, so the API emits `authors: null`:

```sql
SELECT COUNT(*) FROM articles_authors aa
LEFT JOIN authors au ON au.id = aa.author_id
WHERE au.id IS NULL;
```

**Mis-credited articles** — the row is valid, so no integrity check catches it:

```sql
SELECT ar.id FROM articles ar
JOIN articles_authors aa ON aa.articles_id = ar.id
JOIN authors au ON au.id = aa.author_id
WHERE ar.authors NOT LIKE CONCAT('%', au.display_name, '%');
```

**Encoding** — `SELECT HEX(text) ...`, expect `E2809C` for smart quotes.

**Photo URLs** — sample 15–20 and check they return 200. Note the API field is
`featured_image`; the DB column is `photo_url`. Easy false alarm.

**Section pages** — hit `/v1/taxonomy` and load a couple of section routes.
`dev.thetriangle.org` sits behind Cloudflare Access, so page-level checks must
happen in a browser, not from curl.

### Expected residue that is *not* a bug

- ~1200 `www.thetriangle.org` URLs — article cross-links in body text.
- 2 `therectangle.org` URLs — also article links (the defunct predecessor
  domain), not media.
- `entertainment` counts far fewer than its 2543 `Arts & Entertainment`
  articles: listing resolves slug → canonical title, counting buckets by
  `CanonicalizeSlug(category)`. Different keys, so a section can list correctly
  while its count is wrong. Cosmetic.

## 8. Quick reference

| Thing | Value |
| --- | --- |
| ETL repo | `wordpress-etl`, export at `Data/wp-export.zip` |
| ETL output | `logs/sql/{articles,authors,articles_authors,seo,comments,article_embeddings}.sql` |
| DB1 | `10.248.40.154`, `tadmin` NOPASSWD sudo, load from workstation |
| Delta (app host) | `tadmin@10.248.40.168` — reaches DB via MaxScale `10.248.40.183:4006` |
| WP VM (media source) | `tadmin@10.248.40.141`, docroot `/var/www/html/thetriangle.org/wp-content/uploads/` |
| Media on Delta | `/mnt/cephfs/media/wp-content/uploads/` |
| Deploy | merge to `main` → auto-deploys in ~3 min |
