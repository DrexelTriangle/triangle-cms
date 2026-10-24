# Newsletter API

The dashboard's Newsletter screens (`frontend/src/pages/newsletterView.tsx`,
`editNewsletterView.tsx`) are built against this contract. By default they run on
an in-browser mock (`frontend/src/lib/newsletter/mockStore.ts`); build with
`VITE_NEWSLETTER_API=live` to point them at these endpoints.
Types: `frontend/src/lib/newsletter/types.ts` (dashboard) and
`server/internal/models/newsletter.go` (server).

**Status (2026-10-23):** CRUD, scheduling state, stats and the subscriber list are
implemented in Go (`server/internal/handlers/newsletters.go`,
`server/internal/database/newsletters.go`). Nothing sends yet: a scheduled issue
just sits there, and `POST /{id}/test` answers 501.

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
can fill them.

## Endpoints

All require a dashboard session with the editor or admin role, like `/v1/articles`.
Deleting a subscriber is admin-only.

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
| POST | `/v1/newsletters/{id}/test` | `{emails: string[]}` | 204 once a sender exists; **501 for now** |
| GET | `/v1/newsletter-subscribers?status=` | | `{subscribers: [{id, email, name, status, lists, subscribed_at, unsubscribed_at?}]}`; `status` defaults to `confirmed`, or `unsubscribed`/`bounced`/`all` |
| DELETE | `/v1/newsletter-subscribers/{id}` | | 204; erases the row (not an unsubscribe) |

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
- **Public subscribe.** Scalene's `src/pages/subscribe.astro` still posts to the
  dead WordPress Newsletter plugin (`cms.thetriangle.org/wp-json/newsletter/v1/subscribe`).
  It needs a public, rate-limited, double-opt-in `POST /v1/newsletter-subscribers/subscribe`.
- **Subscriber import** from `wp_newsletter` (599 rows, `list_1`..`list_7`) into
  `newsletter_subscribers`, keyed on `legacy_wp_id`. The schema is ready; the
  importer isn't written yet.
- **Opens.** Open tracking needs a pixel or the ESP's webhooks; `open_count` stays 0 until then.
- **Scheduling worker** that moves `scheduled` → `sending` → `sent` and is safe across
  the blue/green slots (only one slot may send).
- **Sanitising.** `body_html`/`email_html` come from trusted dashboard users, but the
  view-online page should still serve them under a CSP, as article content is.
