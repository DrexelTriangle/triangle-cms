# Newsletter API

The dashboard's Newsletter screens (`frontend/src/pages/newsletterView.tsx`,
`editNewsletterView.tsx`) are built against this contract. By default they run on
an in-browser mock (`frontend/src/lib/newsletter/mockStore.ts`); build with
`VITE_NEWSLETTER_API=live` to point them at these endpoints.
Types: `frontend/src/lib/newsletter/types.ts` (dashboard) and
`server/internal/models/newsletter.go` (server).

**Status (2026-10-25):** CRUD, scheduling state, stats, the subscriber list, test
sends and the public unsubscribe page are implemented in Go
(`server/internal/handlers/newsletters.go`, `newsletter_unsubscribe.go`,
`server/internal/mailer/`). The send worker (`server/internal/newsletter/`, see
"Sending" below) sends scheduled issues through the Gmail API
(`mailer.GmailSender`), or SMTP to Mailpit in the laptop sandbox. Open/click
tracking, the view-online page and public subscribe are in. Links in emails
WordPress already sent are handled at Delta's nginx (see the handoff). Not built:
the view-online form of WordPress's legacy links.

## Handoff (2026-10-25)

Where the cutover from WordPress's Newsletter plugin stands. This replaces the
2026-10-24 handoff. Nothing is merged or deployed yet. WordPress is still live and
in editorial use.

### Open PRs, in merge order

| PR | Branch → base | What |
|---|---|---|
| triangle-cms #258 | `feat/newsletter-editor` → `main` | Editor, send worker, Gmail sender, template, tracking, public pages |
| triangle-cms #260 | `feat/newsletter-editor-email-assets` → #258's branch | Masthead and icons served from Delta, trimmed editor copy, the editing tools below |
| triangle-cms #259 | `feat/legacy-redirects` → `main` | `legacy_redirects` table, `/v1/legacy-redirects`, the redirect builder, old-email click links |
| triangle-infrastructure #17 | `feat/legacy-newsletter-unsubscribe` → `main` | Delta nginx: old emails' unsubscribe links |
| triangle-infrastructure #18 | `feat/legacy-redirects-vhost` → #17's branch | Delta nginx: cms.thetriangle.org redirects, map sync timer, old emails' click links |
| Scalene #172 | `feat/newsletter-email-assets` → `dev` | `/newsletter/*` proxy to the CMS (every reader link goes through it) |
| Scalene #173 | `feat/cms-newsletter-subscribe` → `dev` | `/subscribe` form posts to the CMS |
| triangle-sandbox #1 | `feat/newsletter-sandbox` | Mailpit, Gmail test sends with the VPN up, no-login dashboard |

### Done

- **Sending.** Gmail API with WordPress's own OAuth token for notify@. Test-sent to
  a real inbox: SPF, DKIM and DMARC pass, and Reply-To is newsletter@.
- **Template.** It matches the issues WordPress sent. The masthead (`unnamed.png`)
  and the X and Instagram icons load from
  `https://delta.thetriangle.org/wp-content/email/v1/`. They're already published on
  CephFS. The folder is versioned because `/wp-content/` is cached for 30 days, so to
  change an image, add `v2/`. Every image links somewhere (a story photo to its
  story, a comic to its post), and reader links say `www.thetriangle.org/newsletter/...`.
- **New-issue dialog.** Stories and comics are each an ordered list: add, swap
  (pencil), reorder and remove. It starts with the five latest stories and the
  latest comic.
- **Editor.** Article cards are found by their shape: a headline down to its "Click
  Here to Read More" link. Comics are linked images outside any card. The code is in
  `frontend/src/components/trixCards.ts`, and the controls are opt-in through
  `TrixEditor`'s `articleCards` prop.
  - Swapping: each card and comic has a pencil that swaps it in place.
  - Moving: dragging a card's photo, or using its toolbar arrows, moves the whole
    card. A drop can only land between cards.
  - Inserting: "Insert article" goes after the card the cursor is in. "Insert comic"
    goes after the last comic.
  - Each action is one undo step.
  - The article picker lists scheduled stories (soonest first) above published
    ones.
- **Image moves in all editors.** Moves splice Trix's blocks instead of reparsing
  the document. Reparsing used to re-import every off-site image, and delete any
  image it couldn't fetch. Images the newsletter inserts itself are never imported.
- **Tracking.** Tested end to end in the sandbox: every link becomes
  `/c/<token>/<link>`, and an open pixel is added. Test copies are tracked per copy
  but never counted in an issue's totals.
- **Old emails.** WordPress tracker links (`admin-ajax.php?action=tnptr&nltr=...`)
  go through `/v1/legacy-redirects/newsletter-click` (#259, routed by infra #18). It
  checks the plugin's signature, or allows unsigned links to our own domains.
  Unsubscribe links (`action=tnp`) are rewritten by infra #17.

### Before the first Delta send

1. **Scalene #172 merged and deployed to production.** Without it, every reader link
   in an email 404s, including click tracking, unsubscribe and view online.
2. **Delta's backend env. Done 2026-10-25** in `/etc/triangle-cms/cms.env`
   (backup `cms.env.bak-20261025`). Containers read it at deploy, so it takes
   effect when #258 ships.
   - `NEWSLETTER_GMAIL_CLIENT_ID`, `_CLIENT_SECRET` and `_REFRESH_TOKEN`, from
     WordPress's `wp option get wp_mail_smtp`, the `gmail` keys.
   - `NEWSLETTER_PUBLIC_URL=https://delta.thetriangle.org`.
   - `NEWSLETTER_LINK_URL=https://www.thetriangle.org/newsletter`.
   - `NEWSLETTER_LEGACY_LINK_KEY`, the plugin's `newsletter_statistics.key` (not
     `newsletter_main`, which has no key: `statistics.php` signs with the
     Statistics module's option). Without it, only old links to our own domains
     are followed.
   - Leave `NEWSLETTER_SMTP_ADDR` unset: setting both refuses to start.
3. **#258 and #260 deployed,** with the dashboard built `VITE_NEWSLETTER_API=live`.
   Today Delta answers 404 on `/v1/newsletter/*`.
4. **The ad re-uploaded through the sponsor picker.** In WordPress, ads are pasted
   per issue: an image plus the advertiser's link, with no alt text. Issues 47-51 ran
   Google Gemini (with Google's `dclid` tracking URL), and issue 52 ran vote.pa.
5. **`/subscribe`'s iframe pointed at `/v1/newsletter/issues/latest`** once Delta has
   sent an issue.

Taking WordPress offline is a separate step: run the redirect builder with `--apply`,
rsync media (including `newsletter/`), get editors off WordPress, and change the
tunnel route (needs approval). The steps are in triangle-infrastructure's
`docs/legacy-redirects.md`.

### Open decisions

- **WordPress subscribers.** The sandbox import was removed on purpose on
  2026-10-24. Re-importing (`cmd/import-newsletter-subscribers`) is what makes the
  old emails' unsubscribe links mean anything. Without it, the list starts from
  people who subscribe on the new form.
- **Staging.** `dev.thetriangle.org` isn't in Scalene's `security.allowedDomains`, so
  the unsubscribe button's POST through the proxy is refused there. Links and pages
  work.

### Testing in the laptop sandbox (triangle-sandbox #1)

- **Current images.** Both CMS slots run images built from this worktree. To pick up
  frontend changes, rebuild:
  `docker build -t triangle-sandbox/cms-frontend:<CMS_IMAGE_TAG from data/local.env> --build-arg VITE_PUBLIC_SITE_URL=http://www.127.0.0.1.sslip.io:18100 --build-arg VITE_NEWSLETTER_API=live frontend`.
  Then recreate `frontend-blue` and `frontend-green`.
- **Dashboard.** It's at `http://cms.127.0.0.1.sslip.io:18100/newsletter` and is
  signed in automatically (`SANDBOX_AUTOLOGIN_SID`).
- **Test sends** go through Gmail and only to ssavutu@gmail.com. They need
  `python3 -I scripts/gmail-proxy.py --unix data/run/gmail-proxy.sock` running from
  triangle-sandbox. With the VPN up, that proxy is the only way out.
- **Opens from Gmail never register in the sandbox.** Google's image proxy can't
  reach the laptop. Clicks do register.
- **Images.** Article images point at delta.thetriangle.org, whose Cloudflare
  hotlink protection refuses other sites' Referers. The sandbox gateway sends
  `Referrer-Policy: no-referrer` so they load.
- **Test drafts.** Leftover test drafts are #13 and #14.

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

Sender config (backend env): either `NEWSLETTER_GMAIL_CLIENT_ID`,
`NEWSLETTER_GMAIL_CLIENT_SECRET` and `NEWSLETTER_GMAIL_REFRESH_TOKEN` (production:
the Gmail API as the token's mailbox) or `NEWSLETTER_SMTP_ADDR` (host:port, the
sandbox's Mailpit); setting both refuses to start, neither = no sender.
`NEWSLETTER_FROM` (default `The Triangle <notify@thetriangle.org>`; Gmail rewrites a
From that isn't the mailbox or a verified alias), `NEWSLETTER_REPLY_TO` (default
`newsletter@thetriangle.org`, as WordPress's issues had; readers reply to the question
of the day),
`NEWSLETTER_PUBLIC_URL` (origin readers reach `/v1` on; required with a sender; the
List-Unsubscribe header and open pixel use it),
`NEWSLETTER_LINK_URL` (base of every link a reader sees: unsubscribe page, view online,
click redirects; production `https://www.thetriangle.org/newsletter`, which Scalene's
`src/pages/newsletter/[...path].ts` proxies to `/v1/newsletter`; unset = the CMS's own
`/v1/newsletter`. The public pages use relative links and form actions so they work
under either prefix),
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
(KB: access.md); `mailer.GmailSender` does the same from Go.

**Gmail sender.** It reuses the OAuth client and refresh token WordPress's WP Mail
SMTP plugin sent with (`wp option get wp_mail_smtp`, keys `gmail.client_id`,
`gmail.client_secret`, `gmail.refresh_token`; scope `https://mail.google.com/`,
mailbox notify@). Copy them before WordPress is retired; they stay valid after, as
long as the Google Cloud OAuth client isn't deleted. The access token is cached and
refreshed a minute before its hour is up. A message goes to the media upload form of
`users.messages.send` as the RFC 5322 bytes. Retried: anything before the request
went, 401 (token refetched), 403 and 429 (rate and daily quota) and 5xx (a rare
duplicate beats failing the rest of the list in an outage). Not retried: 400 (a bad
recipient), and a connection dropped after the whole request went (it may have been
delivered). A revoked refresh token (`invalid_grant`) defers each copy, so after
three ticks the copy at the head of the queue fails; mint a new token for notify@.

## Still to decide on the backend

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
- **Sanitising.** `body_html`/`email_html` come from trusted dashboard users, but the
  view-online page should still serve them under a CSP, as article content is.
