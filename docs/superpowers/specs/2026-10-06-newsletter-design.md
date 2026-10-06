# Newsletter: data layer, admin API, public subscribe, real admin page

Status: approved design, pre-plan. Date: 2026-10-06. Branch: `feat/newsletter`.

Notion: "Build the newsletter backend (subscribers + campaigns)", "Wire the Newsletter
page to a real API and delete the mock data". Later work this must fit: "Actually send
newsletter campaigns".

## Intent

Give Delta a real newsletter data model and admin page so the WordPress Newsletter
plugin can be retired later. This session builds storage, the admin CRUD, a hardened
public subscribe endpoint, and the dashboard page. **No email is sent.** The schema
must let the later send work resume a half-finished send without double-sending.

Success:
- No invented data anywhere; every number on the page comes from the database.
- Unsubscribe is a state change with a stable token; no API path deletes on unsubscribe.
- The send log design makes a second send to the same subscriber for one campaign
  impossible at the database level.
- Tests assert behaviour (row state, tokens, list membership, counts), not status codes alone.

## Decisions (with Sachin, 2026-10-06)

| Topic | Decision |
|---|---|
| Open/click tracking | **Out.** No pixel, no redirect links, no tracking columns, no open-rate tile. |
| Opt-in | **Single opt-in.** Public subscribes are immediately `subscribed`. |
| Lists | **Real lists**; campaigns target one or more lists. |
| Subscriber access | **Editors and admins.** Hard delete (erasure) is admin-only. |
| Scheduling | **Blocked** until sending exists: `scheduled`/`sent` transitions return 409. |
| WordPress port | Lists, subscribers and the HTML template come over **after** the editor is scaffolded. Nothing is seeded now. |
| Live subscribe path | Scalene keeps posting to WordPress. The new endpoint is built and tested but has no caller until a deliberate cutover. |
| `/newsletter` mock route | Pulled separately in PR #245; this branch re-adds it with the real page. |
| Spoofable client IP | Fixed in a separate PR (rightmost untrusted `X-Forwarded-For` hop + `TRUSTED_PROXIES`). Newsletter uses `middleware.RateLimitByIP` and inherits the fix. |
| Renderer prior art | Dead. The HTML template is ported from WordPress with the send work. |

## Schema

Additive only. One `CREATE TABLE IF NOT EXISTS` per file in
`server/internal/database/schema/`, executed by `EnsureNewsletterTables` in
`server/internal/database/newsletter.go`, followed by an `ADD COLUMN IF NOT EXISTS`
block per table (same pattern as `EnsureClassifiedsTable`). Called from `main.go`
next to the classifieds/wordangle ensures. All tables `ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`.

### `newsletter_lists`
| column | type | notes |
|---|---|---|
| id | BIGINT UNSIGNED PK AUTO_INCREMENT | The WP port inserts explicit ids 1..7 so Scalene's checkbox values map 1:1. |
| name | VARCHAR(128) NOT NULL | |
| description | VARCHAR(255) NULL | |
| is_public | TINYINT(1) NOT NULL DEFAULT 1 | Whether the public form may subscribe to it. |
| created_at, updated_at | DATETIME | standard defaults |

Unique index on `name`.

Port caveat: lists created by hand before the port take ids 1..7. The port must
check for that, or lists should be created only via the port on prod.

### `newsletter_subscribers`
| column | type | notes |
|---|---|---|
| id | BIGINT UNSIGNED PK | |
| email | VARCHAR(254) NOT NULL | Trimmed and lowercased before storage. `UNIQUE`. |
| name | VARCHAR(100) NULL | |
| status | VARCHAR(16) NOT NULL DEFAULT 'subscribed' | `subscribed` \| `unsubscribed`. VARCHAR so later states are additive. |
| token | CHAR(43) NOT NULL | 32 bytes `crypto/rand`, base64url, no padding. `UNIQUE`. Set on insert, never rotated. Never returned by the public endpoint. |
| source | VARCHAR(32) NOT NULL | `public_form` \| `admin` \| `wordpress_import` (source of the latest (re)subscribe). |
| signup_ip | VARCHAR(45) NULL | IP of the latest public (re)subscribe; for cleaning up bot floods. |
| subscribed_at | DATETIME NOT NULL | Latest (re)subscribe. |
| unsubscribed_at | DATETIME NULL | Cleared on resubscribe. |
| created_at, updated_at | DATETIME | |

Index on `(status)`.

### `newsletter_subscriber_lists`
`(subscriber_id, list_id)` primary key, index on `list_id`. Foreign keys to both tables with
`ON DELETE CASCADE`, so an erasure delete also removes the subscriber's memberships.

### `newsletter_campaigns`
| column | type | notes |
|---|---|---|
| id | BIGINT UNSIGNED PK | |
| subject | VARCHAR(255) NOT NULL | |
| preview_text | VARCHAR(255) NULL | Email preheader. |
| body_html | LONGTEXT NOT NULL | Content only. The ported WP template wraps it at send time. |
| status | VARCHAR(16) NOT NULL DEFAULT 'draft' | `draft` \| `scheduled` \| `sent`. The send work adds `sending`. |
| scheduled_at | DATETIME NULL | |
| sent_at | DATETIME NULL | |
| recipient_count | INT UNSIGNED NULL | Captured once at send start. Never recomputed. |
| created_by, updated_by | VARCHAR(255) NULL | User emails, as in classifieds `decided_by`. |
| created_at, updated_at | DATETIME | |

Index on `(status, scheduled_at)`.

### `newsletter_campaign_lists`
`(campaign_id, list_id)` primary key, with foreign keys and `ON DELETE CASCADE`.

### Send log (designed now, created by the send work)
```sql
CREATE TABLE IF NOT EXISTS newsletter_campaign_sends (
  campaign_id BIGINT UNSIGNED NOT NULL,
  subscriber_id BIGINT UNSIGNED NOT NULL,
  email VARCHAR(254) NOT NULL,          -- snapshot at send start
  status VARCHAR(16) NOT NULL DEFAULT 'queued',  -- queued | sent | failed
  attempts INT UNSIGNED NOT NULL DEFAULT 0,
  last_error VARCHAR(1024) NULL,
  provider_message_id VARCHAR(255) NULL,
  sent_at DATETIME NULL,
  PRIMARY KEY (campaign_id, subscriber_id),
  INDEX idx_newsletter_sends_status (campaign_id, status)
)
```
Flow:
1. Set the campaign to `sending`.
2. `INSERT IGNORE … SELECT DISTINCT` the subscribed members of the target lists as
   `queued` rows; `recipient_count` = the resulting row count.
3. Workers claim batches with `SELECT … WHERE status IN ('queued','failed') … FOR UPDATE
   SKIP LOCKED` (MariaDB ≥10.6; local and prod run 11.x), send, then mark each row `sent` or
   `failed` (`attempts+1`) in the same transaction.
4. A resume processes only `queued`/`failed` rows.

The primary key makes a duplicate recipient row impossible. MaxScale caveat: re-read
rows rather than trusting `ROW_COUNT()`.

Nothing in this session's schema conflicts with that flow. The recipient selection
query (`RecipientCountForCampaign`) is written now and reused by the send step.

## Subscriber state rules

| Situation | Effect |
|---|---|
| New address | Insert `subscribed` with a new token and the submitted lists. |
| Already `subscribed` | Add the submitted lists to the existing ones (union). Keep the token. Update `name` if one is given. |
| `unsubscribed`, then subscribes again | Same row. Status becomes `subscribed`, `subscribed_at=now`, `unsubscribed_at=NULL`. Lists are **replaced** by the submitted set, the name is replaced, and the token is unchanged. |
| Admin/editor unsubscribes | Status becomes `unsubscribed`, `unsubscribed_at=now`. Row, token and memberships are kept. |
| Admin hard delete | Removes the row and its memberships (erasure requests only). |

Known trade-off of single opt-in: anyone can subscribe, or re-subscribe, any address.
No mail goes out yet, so nothing reaches the address. The send work must revisit this
before the first real send (confirmation mail, or ignoring public resubscribes of
addresses that explicitly unsubscribed).

## API

All JSON. Handlers in `server/internal/handlers/newsletter.go`, DB in
`server/internal/database/newsletter.go`, models in `server/internal/models/`. Swag
annotations, plus a `newsletter` `@tag.name` in `main.go`. `swag init` output is committed.
Errors use `writeError` (`{"error": "..."}`). Mutations log via `activity.LogRequest`.

### Public
`POST /v1/newsletter/subscribe`

Request: `{"email": string, "name"?: string, "lists": [int], "website"?: string}`

Middleware, outermost first:
1. `RateLimitByIP(20, time.Hour)`
2. `RateLimitByIP(5, time.Minute)`
3. A new `RateLimitGlobal(300, 10*time.Minute)` in `middleware/rate_limit.go`, a
   single shared fixed-window counter.

Handler:
1. `http.MaxBytesReader` 4 KB. Decode JSON (unknown fields allowed; Scalene may send extras).
2. Honeypot: if `website` is non-empty, return the success response and store nothing.
3. Validate (generic `400 {"error":"invalid subscription"}` for any failure; detail logged at debug):
   - email: trimmed; ≤254 bytes; no control or whitespace characters; `mail.ParseAddress` succeeds
     and its `Address` equals the input (rejects display names, comments, angle brackets);
     exactly one `@`; local part ≤64; domain contains a `.` and no leading/trailing `.`/`-`.
     Lowercased.
   - name: optional; trimmed; ≤100 runes; no control characters; valid UTF-8.
   - lists: 1–20 entries after de-duplication; each a positive integer; every id must
     be an existing `is_public` list (one `SELECT COUNT(*) … WHERE id IN (…) AND is_public=1`).
4. Akismet (`spamChecker`, may be nil): `CheckComment` with `Type: "signup"`, the email,
   the name, the IP, the UA and the referrer. Spam → success response, nothing stored, info log.
   Error → proceed and log (warn; error for `ConfigError`), as comments do.
5. Upsert per the state rules, in one transaction.
6. Always `202 {"ok": true}`, whether the address was new, already subscribed,
   resubscribed, flagged as spam or caught by the honeypot. The response never reveals
   whether an address is on file. DB failure → `500` with a generic message (no `err.Error()` leakage).

Future caller: a Scalene server-side API route (like `api/comments.ts`) so the client IP
arrives through a trusted proxy and no CORS is needed. That is part of the cutover, not this work.

### Authenticated (`authMW`; editor or admin unless marked)

| Method & path | Notes |
|---|---|
| `GET /v1/newsletter/stats` | `{subscribers: {subscribed, unsubscribed}, campaigns: {draft, scheduled, sent}, lists: [{id, name, subscribed}]}` |
| `GET /v1/newsletter/lists` | All lists with subscribed-member counts. |
| `POST /v1/newsletter/lists` | **admin**. `{name, description?, is_public?}`. 409 on duplicate name. |
| `PATCH /v1/newsletter/lists/{id}` | **admin**. Partial update. No list delete. |
| `GET /v1/newsletter/subscribers` | `?status=subscribed\|unsubscribed\|all&list_id=&q=&page=&limit=`. `q` matches email/name (LIKE, escaped). Returns `{subscribers, pagination, counts}`. Each subscriber has its `list_ids`. Token not returned. |
| `POST /v1/newsletter/subscribers` | `{email, name?, list_ids}`. Same validation as public, `source=admin`. 409 if the email exists (the caller should PATCH instead). |
| `PATCH /v1/newsletter/subscribers/{id}` | `{name?, status?, list_ids?}`. Status changes follow the state rules (timestamps). |
| `DELETE /v1/newsletter/subscribers/{id}` | **admin**. Hard delete for erasure. 204. |
| `GET /v1/newsletter/campaigns` | `?status=&q=&page=&limit=`. List rows omit `body_html`. Includes `list_ids` and `counts`. |
| `GET /v1/newsletter/campaigns/{id}` | Full campaign. |
| `POST /v1/newsletter/campaigns` | `{subject, preview_text?, body_html, list_ids}`. Always created as `draft`. |
| `PATCH /v1/newsletter/campaigns/{id}` | Partial update of content, `list_ids` and `scheduled_at`. **409** if the campaign is `sent`. **409** "sending isn't available yet" if `status` is set to anything but `draft`. |
| `DELETE /v1/newsletter/campaigns/{id}` | **admin**. Drafts only; a `sent` campaign is a permanent record (409). |
| `GET /v1/newsletter/campaigns/{id}/recipients/count` | `{count}`: distinct `subscribed` members of the campaign's lists. Same query the send step will use. |

Validation limits: subject 1–255; preview ≤255; `body_html` ≤ 1 MB; `list_ids` must exist.
`body_html` is stored as given; it is editor-authored and is not rendered by the dashboard
except inside a sandboxed preview, if one is added.

No send endpoint exists.

## Dashboard page (`frontend/src/pages/newsletterView.tsx`)

- All data comes through `useApiFetch`. The `CAMPAIGNS`/`SUBSCRIBERS` constants are deleted.
- Tiles: **Subscribed**, **Unsubscribed**, **Drafts**, **Sent**, all from `/stats`. No open-rate or "recipients reached" tile.
- Campaigns tab: table (subject, preview, status, target lists, recipients = `recipient_count` or "—", updated date). New/Edit opens a form: subject, preview text, HTML body (textarea for now; the editor is scaffolded later), and list multi-select. Delete is admin-only, drafts only, with a confirm step.
- **Send**: opens a confirmation dialog that fetches `/recipients/count` and says
  "Would reach **N** subscribers in *List A, List B*". The confirm button is disabled
  with "Delivery isn't built yet." No request is made beyond the count.
- Subscribers tab: server-side search, a status filter and a list filter. Add subscriber, unsubscribe/resubscribe toggle, edit lists. Delete (admin, with confirm) is labelled as an erasure.
- Lists live in a small section or modal (admin can add/rename/toggle public).
- Loading, empty and error states are shown explicitly; there are no fallbacks to fake data.
- Re-add the route in `App.tsx`, and the sidebar entry plus `Mail` import in `Sidebar.tsx`.

## Testing

Go (`server/internal/...`; integration tests use `CMS_TEST_DSN` → local `tax_test`, run with `-p 1`):
- **Validator unit tests (hostile table):** display-name addresses, CRLF injection,
  whitespace and control characters, 255+ byte addresses, Unicode lookalikes, no TLD,
  multiple `@`, list ids that are 0/negative/duplicated/non-public/unknown/21+, an oversized name, a body over 4 KB (400, nothing stored).
- **DB integration:**
  - the token is set on insert, unique, and unchanged across unsubscribe and resubscribe
  - unsubscribe keeps the row and memberships
  - resubscribe replaces lists and clears `unsubscribed_at`
  - a duplicate subscribe unions lists
  - the email is stored lowercased, and differently cased addresses map to one row
  - the recipient count de-duplicates across overlapping lists and excludes unsubscribed subscribers
- **Handler integration (httptest + real DB):**
  - the public response body is byte-identical for new, duplicate, honeypot and Akismet-spam
    submissions, while the DB differs as expected (a row exists or doesn't)
  - a fake Akismet error still stores the subscriber
  - a sent campaign PATCH returns 409 and the row is unchanged
  - setting status to `scheduled` returns 409 and the row stays `draft`
  - an editor gets 403 on subscriber DELETE and the row still exists
- **Middleware:** `RateLimitGlobal` allows N requests then returns 429, and resets after the window (injectable clock).

Vitest (`newsletterView.test.tsx`, mocked `fetch`):
- the tiles render the API numbers, and no open-rate text appears
- the send dialog shows the fetched count and list names, its confirm button is disabled,
  and no POST is made
- creating a campaign sends the expected JSON body
- an unsubscribe toggle PATCHes `{status:"unsubscribed"}` and the row updates
- an API error renders the error state and no table rows.

Run frontend tests with `NODE_ENV=test` (the shell exports `production`).

## Out of scope

Delivery, the confirmation email, the public unsubscribe/confirm endpoints (tokens exist for them),
the WP list/subscriber/template port, the Scalene cutover, the rich editor, tracking, and the XFF fix (separate PR).
