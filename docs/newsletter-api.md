# Newsletter API

The dashboard's Newsletter screens (`frontend/src/pages/newsletterView.tsx`,
`editNewsletterView.tsx`) are built against this contract. By default they run on
an in-browser mock (`frontend/src/lib/newsletter/mockStore.ts`); build with
`VITE_NEWSLETTER_API=live` to point them at these endpoints.
Types: `frontend/src/lib/newsletter/types.ts` (dashboard) and
`server/internal/models/newsletter.go` (server).

**Status (2026-10-24):** CRUD, scheduling state, stats, the subscriber list, test
sends and the public unsubscribe page are implemented in Go
(`server/internal/handlers/newsletters.go`, `newsletter_unsubscribe.go`,
`server/internal/mailer/`). The send worker (`server/internal/newsletter/`, see
"Sending" below) sends scheduled issues; the only sender is still SMTP to Mailpit
in the laptop sandbox. Open/click tracking, the view-online page and public subscribe
are in; WordPress's legacy unsubscribe links are mapped in Delta's nginx
(triangle-infrastructure, branch `feat/legacy-newsletter-unsubscribe`). Not
built: the Gmail API sender, and the view-online form of WordPress's legacy
links (below).

## Model

The newsletter is **To The Point**, The Triangle's emailed digest. An issue is:

| field | type | notes |
| --- | --- | --- |
| `id` | int | |
| `subject` | string | |
| `preheader` | string | inbox preview line, hidden in the body |
| `status` | `draft` \| `scheduled` \| `sending` \| `sent` | server-owned |
| `sponsor` | `{image_url, link_url, alt}` \| null | banner above the masthead |
| `body_html` | string | article-shaped HTML from the Trix editor (same shape as `articles.content`) |
| `email_html` | string | full rendered email, built by the dashboard |
| `scheduled_for` | RFC 3339 \| null | |
| `sent_at` | RFC 3339 \| null | |
| `recipient_count`, `open_count` | int | |
| `created_at`, `updated_at` | RFC 3339 | |

The dashboard renders `email_html` itself (`frontend/src/lib/newsletter/template.ts`)
on every create/save, so the server never needs a copy of the template. The server
stores it verbatim and, at send time, substitutes per recipient:

- `{{unsubscribe_url}}` — one-click unsubscribe (also send `List-Unsubscribe` /
  `List-Unsubscribe-Post` headers)
- `{{manage_subscription_url}}`
- `{{view_online_url}}` — a public web copy of the issue

These are generic tokens, not provider merge tags, so any sender (SMTP or an ESP)
can fill them. `server/internal/mailer` fills them: unsubscribe and manage both go
to `{NEWSLETTER_PUBLIC_URL}/v1/newsletter/unsubscribe?t=<token>` (there is no
separate preferences page), view online to `/v1/newsletter/issues/{id}`. Test sends
use the token `test`, which the unsubscribe page treats as a no-op.

The template is laid out for desktop Outlook (Word): an MSO-only 600px table, block
spacing from cell padding rather than margins, text blocks as `<p>`, no `<figure>`.
Mailpit's HTML Check in the sandbox shows what a change does to client support.

## Endpoints

All require a dashboard session with the editor or admin role, like `/v1/articles`.
Deleting a subscriber is admin-only. The unsubscribe routes below are public.

| method | path | body | returns |
| --- | --- | --- | --- |
| GET | `/v1/newsletters` | | `{newsletters: Newsletter[]}` newest-updated first |
| GET | `/v1/newsletters/stats` | | `{subscriber_count, sent_count, recipients_reached, avg_open_rate}` (`avg_open_rate` 0..1 or null) |
| GET | `/v1/newsletters/{id}` | | `Newsletter` |
| POST | `/v1/newsletters` | `NewsletterInput` | 201 `Newsletter` (status `draft`) |
| PUT | `/v1/newsletters/{id}` | `NewsletterInput` | `Newsletter`; a scheduled issue stays scheduled; 409 once `sending`/`sent` |
| DELETE | `/v1/newsletters/{id}` | | 204; 409 once `sending`/`sent` |
| POST | `/v1/newsletters/{id}/schedule` | `{send_at}` (RFC 3339, future) | `Newsletter` (status `scheduled`); 409 once `sending`/`sent` |
| DELETE | `/v1/newsletters/{id}/schedule` | | `Newsletter` (back to `draft`) |
| POST | `/v1/newsletters/{id}/test` | `{emails: string[]}` | 204; sends the saved issue, subject `[Test] …`, to ≤5 bare addresses; 501 with no sender configured; 502 if the sender fails (the message names who already got it) |
| GET | `/v1/newsletter-subscribers?status=` | | `{subscribers: [{id, email, name, status, lists, subscribed_at, unsubscribed_at?}]}`; `status` defaults to `confirmed`, or `unsubscribed`/`bounced`/`all` |
| DELETE | `/v1/newsletter-subscribers/{id}` | | 204; erases the row (not an unsubscribe) |
| POST | `/v1/newsletter-subscribers/subscribe` | `{email, lists?: int[], website?}` | **public**, 5/min per IP; `{success: true}` for every valid submit (new, already subscribed, or unsubscribed/bounced, which stays so); 400 `{success: false, error}` for a bad address or list id; a filled `website` honeypot answers success and stores nothing |
| GET | `/v1/newsletter/unsubscribe?t=` or `?nk=<wp id>-<wp token>` | | public HTML page; **never changes anything** (mail scanners open every link) |
| POST | `/v1/newsletter/unsubscribe?t=`/`?nk=` | form, or RFC 8058 `List-Unsubscribe=One-Click` | public HTML page; confirmed → unsubscribed; repeating it is harmless |
| POST | `/v1/newsletter/resubscribe?t=`/`?nk=` | | public HTML page; unsubscribed → confirmed; bounced stays bounced |
| GET | `/v1/newsletter/o/{delivery}` | | public; 1×1 GIF, always (even for unknown tokens); counts an open |
| GET | `/v1/newsletter/c/{delivery}/{link}` | | public; 302 to the link, counting a click (and an open); 404 unless the link belongs to the delivery's issue, so it is not an open redirect |

Tracking: every copy sent is a row in `newsletter_deliveries` (random `token`, `is_test`
for test sends, per-copy open/click counts), and each issue's links are registered in
`newsletter_links`. The mailer rewrites every http(s) `href` in the body to the click URL
and adds the pixel before `</body>`; unsubscribe and view-online links are not rewritten.
`newsletters.open_count`/`click_count` count unique readers among real copies; test
copies are tracked but never counted. HEAD requests are never counted (scanners send
them). Opens read high with Apple Mail Privacy Protection and low where images are
blocked; link scanners inflate clicks.

Sender config (backend env): `NEWSLETTER_SMTP_ADDR` (host:port; unset = no sender),
`NEWSLETTER_FROM` (default `The Triangle <notify@thetriangle.org>`),
`NEWSLETTER_PUBLIC_URL` (origin readers reach `/v1` on; required with a sender),
`NEWSLETTER_SITE_URL` (the unsubscribe page's link home; default www.thetriangle.org),
`NEWSLETTER_SEND_RATE` (messages per second, default 2, at most 50),
`NEWSLETTER_DAILY_CAP` (most copies in a rolling 24 hours, default 1500).

## Sending

With a sender configured, every backend slot runs `newsletter.Worker` (every 15s);
the state is all in the database (`server/internal/database/newsletter_send.go`).

- **Start.** A `scheduled` issue whose `scheduled_for` has passed moves to `sending`
  and gets one `pending` delivery per confirmed subscriber, in one transaction.
  Campaigns go to all confirmed subscribers, not per list, as in WordPress. An
  issue more than 12h overdue (the sender was down) is left `scheduled` and logged:
  stale news needs an editor to schedule it again.
- **One sender.** The issue row carries a lease (`send_lease_owner`/`until`, 2 min),
  taken and renewed under `SELECT … FOR UPDATE`; only the holder claims deliveries.
  A dead slot's lease runs out and the other slot carries on.
- **Each copy.** Claimed `pending` → `sending` before the message goes, then `sent`;
  `failed` for a refusal (SMTP 5xx) or when delivery is unknown (the connection
  dropped after the message went); back to `pending` for a transient error, up to 3
  attempts, which also ends the run until the next tick. A subscriber who
  unsubscribed or was erased after the start is `skipped`. A row still `sending`
  when the lease changes hands belonged to a slot that died mid-message: it is
  marked `failed`, never resent. A gap beats a duplicate.
- **Pace.** `NEWSLETTER_SEND_RATE` per second. Before each claim the worker counts
  `sent` deliveries (all issues, test copies too) in the last 24h, and at
  `NEWSLETTER_DAILY_CAP` the issue stays `sending` until the window frees up. Two
  slots sending two different issues can overshoot the cap by one.
- **Finish.** With nothing `pending` or `sending` left, the issue is `sent`;
  `recipient_count` counts the copies that went.
- **Shutdown.** SIGTERM stops claiming; the message in hand is sent and recorded
  before the process exits, so a deploy mid-send resumes cleanly on the new slot.

Editors can't change, unschedule or delete an issue once it is `sending` (409). There
is no pause or cancel for a send in progress yet, and the dashboard shows no
progress beyond the status.

| GET | `/v1/newsletter/issues/{id}` or `/latest` | | public HTML; a **sent** issue only (drafts 404). Unsubscribe links go to the keyless unsubscribe page, which says to use the link in the inbox copy; not tracked; frameable by `NEWSLETTER_SITE_URL` (Scalene's `/subscribe` iframe, which today loads a static `tothepoint.html`, should point at `/latest`) |

WordPress's links in issues it already sent point at the WordPress plugin's endpoint on
`cms.thetriangle.org`: `/wp-admin/admin-ajax.php?action=tnp&na=<action>&nk=<id>-<token>`
(`na=u`/`uc`/`ocu` unsubscribe, `na=v` view online, plus `id=<email id>`). Delta's
nginx (`roles/delta_cms_host/files/triangle-cms.conf` in triangle-infrastructure)
rewrites `na=u|uc|ocu` in place onto `/v1/newsletter/unsubscribe?nk=…`, keeping the
method so an inbox's one-click POST still unsubscribes; the sandbox gateway mirrors
it. It takes effect only once the tunnel routes `cms.thetriangle.org` to Delta
(today WordPress still answers those links itself). `na=v` 404s: WordPress's sent
issues were not imported.

Public subscribe: Scalene's `/subscribe` form posts to its own same-origin
`/api/newsletter-subscribe`, which forwards to the CMS with the reader's address in
`X-Forwarded-For` (Scalene branch `feat/cms-newsletter-subscribe`). Single opt-in,
as WordPress was. The form can't resubscribe an address that unsubscribed: anyone
can type an address in, so only the reader's own link can undo it.

`NewsletterInput` = `{subject, preheader, sponsor, body_html, email_html}`. `subject`
(≤255) and `email_html` are required; a sponsor with an empty `image_url` is stored
as no sponsor; sponsor URLs must be http(s). Request bodies are capped at 4 MiB.
Errors: JSON `{error: "message"}`; the dashboard shows the message as-is.

## Storage

Tables are created at startup by `EnsureNewsletterTables` from
`server/internal/database/schema/newsletter*.sql`:

- `newsletters`: one row per issue; the sponsor is three nullable columns.
- `newsletter_subscribers`: unique `email`; `status` confirmed/unsubscribed/bounced;
  a random 32-hex `token` for unsubscribe keys; `legacy_wp_id` (unique) for an
  idempotent import from `wp_newsletter`.
- `newsletter_lists`: lists 1-7 seeded with the WordPress names and ids.
- `newsletter_subscriber_lists`: membership, cascading on subscriber delete.

Status-guarded writes (save, delete, schedule) run in a transaction that takes
`SELECT ... FOR UPDATE` on the issue first. MaxScale routes the whole transaction
to the primary, so the check never reads a stale replica, and a future sender that
locks the row the same way can't race an editor's save.

## Parity target: WordPress "Newsletter" plugin

Goal (2026-10-22): match the legacy site's Newsletter plugin (by Stefano Lissa) for
subscription management, campaigns and open/click tracking, then retire it.

Known from the legacy host (`thetriangle-wordpress`, 10.248.40.141, webroot
`/var/www/html/thetriangle.org`, read-only inspection over the VPN):

- Active: `newsletter` 9.0.9 and `wp-mail-smtp` 4.7.1. No Newsletter addons
  (no SMTP/API/Reports/Automated), so everything comes from the free core plugin.
- Sending path: Newsletter hands each message to `wp_mail()`; WP Mail SMTP 4.7.1 sends
  it with its **Gmail API mailer** (OAuth, scope `https://mail.google.com/`) as
  `notify@thetriangle.org`, from name "The Triangle". Newsletter's own sender, reply-to
  and return-path are `newsletter@thetriangle.org` (WP Mail SMTP doesn't force its
  From). Same mechanism as triapply.
- Throttle: `scheduler_max` 200/hour, run as ~16 messages per WP-Cron tick every ~5 min.
  A plugin setting, not a Gmail limit; a 593-recipient issue took ~3 hours. **Don't
  copy it** (Sachin, 2026-10-22): Delta sends an issue in minutes at a configurable
  few/second, with a rolling-24h counter that pauses before Gmail's daily cap
  (~2,000 recipients for Workspace) and resumes when the window frees up.
  No system cron: WP-Cron fires on site traffic (the public site now runs on Scalene, so
  the WP host's own traffic and Matomo keep it ticking).
- Subscription: **single opt-in** (`noconfirmation=1`), multiple lists allowed, privacy
  checkbox on, email-only form (no name). Welcome email (id 7, subject "Welcome") sent on
  subscribe. Unsubscribe page asks for a confirm click, then a "Goodbye" email;
  re-subscribe ("reactivate") link offered. RFC 8058 one-click unsubscribe is used by
  inboxes (`unsubscribe-rfc8058` log source). Profile page lets a subscriber edit their
  email. Antiflood 60s, no captcha/Akismet. Newsletter page = WP page 55382.
- Lists 1-7 = Drexel Students, Alumni, Parents, Administrators/Directors/Trustees,
  Faculty, Advertisers, Other (`list_N` tinyint columns; 8-40 unused). Keep these ids.
- Data (2026-10-22): `wp_newsletter` 599 subscribers (593 confirmed, 6 unsubscribed,
  0 bounced; created 2024-12-28 → 2026-10-17; 1 test row). Confirmed per list: 256 / 186
  / 114 / 74 / 75 / 56 / 90, 55 on no list. Campaigns go to **all confirmed** subscribers
  (`query`: `status='C'`), not per list. Domains: gmail 212, drexel.edu 211, yahoo 38.
- Campaigns: `wp_newsletter_emails`, 40 rows (33 sent `message`s, all "To The Point",
  weekly, Fri 14:00; composer editor = 2, settings in serialized `options` incl.
  `preheader`; ~92 KB HTML each). Issue 52 is scheduled for 2026-10-23 14:00.
- Tracking (`track=1`): `wp_newsletter_sent` one row per recipient per issue
  (`open` 0/1/2 = none/opened/clicked, `error`); `wp_newsletter_stats` one row per event
  (`url=''` = open pixel, else the clicked URL; 23k rows). Links and pixel are added at
  send time, not stored in the HTML; placeholders `{email_url}`, `{profile_url}`,
  `{unsubscription_url}`, `{email_subject}`. Recent issues: ~45-48% open, 10-20% click.
  Top clicks: X/Twitter, articles, the sponsor ad.
- Errors: Gmail API `400 Invalid To header` once per send (20 "Invalid email, skipped"
  rows in `_sent`); no quota errors.
- `custom_newsletter_save` (inactive): an older homegrown admin page that picked posts
  and sent one message via `wp_mail()`. Not used; nothing to port.

Sending constraints already known: no control of thetriangle.org DNS (no SPF/DKIM
changes, so no domain-verified ESP), and Gmail caps a Workspace account at ~2,000
recipients/day (500 for consumer Gmail), so campaigns must be throttled. triapply
already sends as notify@ through the Gmail HTTP API with an OAuth refresh token
(KB: access.md); reusing that pattern from Go avoids SMTP entirely.

## Still to decide on the backend

- **Sender.** Provider is undecided; the stored HTML is provider-neutral.
  Current issues come from `notify@thetriangle.org`.
- **Rate-limit key.** `middleware.RateLimitByIP` trusts the first `X-Forwarded-For`
  hop, so a client that reaches the CMS directly (not through Scalene) can rotate it
  past the limit. It is the same for comments, classifieds and polls; fix it once,
  for all of them.
- **Subscriber import**: `server/cmd/import-newsletter-subscribers` copies
  `wp_newsletter` in, keyed on `legacy_wp_id`. Source from `WP_DSN`, target from
  `DB_*`; one transaction, rolled back unless `-apply`. Re-runnable until WordPress
  is retired: it adds new rows and carries WordPress unsubscribes over, but never
  resubscribes anyone, and copies lists on insert only. Statuses other than C/U/B
  (never confirmed), invalid addresses and later duplicates of an address are
  skipped and listed. The WordPress token is kept in `legacy_wp_token` so old
  `nk=<id>-<token>` unsubscribe links can be honoured after cutover. Prod needs
  the backend deployed first (it refuses without the tables) and operator approval.
  Source checked 2026-10-23: 594 C (1 `test`) + 7 U, no case-insensitive duplicate
  addresses, tokens ≤10 chars, `created` is a `timestamp` on a UTC server. The WP
  DB user is `wordpressuser@%` and Delta reaches 10.248.42.122:3306, so run it on
  Delta. `WP_DSN` needs `parseTime=true`. wp-config writes
  `define( 'DB_USER', '…' );` with spaces inside the parentheses; strip the quotes
  when scripting against it.
- **Opens.** Open tracking needs a pixel or the ESP's webhooks; `open_count` stays 0 until then.
- **Sanitising.** `body_html`/`email_html` come from trusted dashboard users, but the
  view-online page should still serve them under a CSP, as article content is.
