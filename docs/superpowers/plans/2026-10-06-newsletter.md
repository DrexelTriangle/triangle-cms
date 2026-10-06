# Newsletter Data Layer, Admin API and Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Real newsletter storage (lists, subscribers, campaigns), admin CRUD, a hardened public subscribe endpoint, and a dashboard page with no mock data. No email delivery.

**Architecture:** Five CMS-owned tables created at startup from `schema/*.sql` via `EnsureNewsletterTables`. DB functions live in `internal/database/newsletter_*.go`, handlers in `internal/handlers/newsletter*.go`, and models in `internal/models/newsletter.go`. Handlers follow the classifieds shape. The React page talks to the API through `useApiFetch`.

**Tech Stack:** Go 1.25 `net/http` mux, MariaDB 11 (`go-sql-driver/mysql`), swaggo; React 19 + TS + Vite + Vitest + Testing Library.

**Spec:** `docs/superpowers/specs/2026-10-06-newsletter-design.md`. Read it first. Field sizes, state rules and response shapes are defined there and not repeated here.

## Global Constraints

- Schema is additive only. `CREATE TABLE IF NOT EXISTS` plus an `ADD COLUMN IF NOT EXISTS` block per table. Never DROP/retype. Only the local docker stack (`tax_test` for tests); never a remote DB.
- `newsletter_lists` has `AUTO_INCREMENT=1000`.
- Foreign keys use `ON DELETE CASCADE` on both join tables (pattern: `polls.go:135`).
- Tokens are 32 bytes `crypto/rand` encoded as base64url without padding (43 chars), set on insert and never rotated. They are never present in any JSON response.
- Public subscribe always answers `202 {"ok":true}` for accepted, duplicate, honeypot and spam submissions; `400 {"error":"invalid subscription"}` for any validation failure; `500 {"error":"subscription failed"}` for a DB error (never `err.Error()`).
- Status transitions to `scheduled`/`sent` return `409 {"error":"sending isn't available yet"}`. There is no send endpoint.
- Roles: editor+ (plain `authMW`) everywhere except `adminOnly` on: list create/patch, subscriber DELETE, campaign DELETE.
- No tracking columns, no open-rate UI.
- Integration tests skip without `CMS_TEST_DSN`. Run them with `go test ./internal/... -p 1`.
- Frontend: `npm ci --include=dev` once; run tests with `NODE_ENV=test npm test` (the shell exports `production`, which breaks React test builds).
- Swagger: every new handler is annotated with `@Tags newsletter`. Add `// @tag.name newsletter` in `main.go`, run `swag init --parseDependency --parseInternal` in `server/`, and commit `server/docs/`.
- Commit after every task on `feat/newsletter`. Never on `main`.

## Review Focus

1. **Mixed-case duplicates:** `Foo@Example.com` then `foo@example.com` must hit one row, not trip the UNIQUE index into a 500. → Task 3 test `TestSubscribePublic_CaseInsensitiveEmail`.
2. **Concurrent first subscribes of the same address:** a race on INSERT must not 500. Use `INSERT … ON DUPLICATE KEY UPDATE`, not SELECT-then-INSERT. → Task 3 test `TestSubscribePublic_ConcurrentSameEmail` (10 goroutines → 1 row, 0 errors).
3. **`q` search containing `%` or `_`** must match literally. → Task 3 test `TestListSubscribers_QueryEscapesWildcards`.
4. **Campaign targeting a list that later goes non-public** still counts recipients (public-ness only gates the public form). → Task 4 test `TestRecipientCount_IgnoresListPublicFlag`.
5. **Page against an empty DB** (no lists, no campaigns) shows empty states and a disabled "New campaign" hint about creating a list, not a crash. → Task 9 test `renders empty states with no lists`.

---

### Task 1: Schema and `EnsureNewsletterTables`

**Files:**
- Create: `server/internal/database/schema/newsletter_lists.sql`, `newsletter_subscribers.sql`, `newsletter_subscriber_lists.sql`, `newsletter_campaigns.sql`, `newsletter_campaign_lists.sql`
- Create: `server/internal/database/newsletter.go`
- Modify: `server/main.go` (after `EnsureWordangleTable`, same fatal-on-error shape)
- Test: `server/internal/database/newsletter_integration_test.go`

**Interfaces:**
- Produces: `func EnsureNewsletterTables(ctx context.Context, conn *sql.DB) error`. Tables are created parents-first. It also produces the test helper `newsletterTestDB(t) *sql.DB`, which drops the 5 tables children-first, then calls `EnsureNewsletterTables`. Later DB tasks reuse it.

- [ ] **Step 1: Write the failing tests**
  - `TestEnsureNewsletterTables_Idempotent`: call `EnsureNewsletterTables` twice and get no error. All 5 tables exist (`information_schema.tables`).
  - `TestNewsletterLists_AutoIncrementStartsAt1000`: a plain `INSERT INTO newsletter_lists (name) VALUES ('Hand made')` gets `id >= 1000` (re-read by name, not via `LastInsertId`); a subsequent `INSERT … (id, name) VALUES (1,'Drexel Students')` succeeds and both rows exist.
  - `TestNewsletterJoinRows_CascadeOnDelete`: insert a list, a subscriber and a membership; delete the subscriber; the membership count is 0. Repeat for campaign and campaign_lists.
- [ ] **Step 2: Run** `CMS_TEST_DSN=… go test ./internal/database/ -run 'Newsletter' -p 1 -v`. Expected: FAIL (undefined `EnsureNewsletterTables`).
- [ ] **Step 3: Write the 5 schema files exactly per the spec's Schema section** (column types, defaults, `UNIQUE(email)`, `UNIQUE(token)`, `UNIQUE(name)`, indexes, `AUTO_INCREMENT=1000` on lists, FK constraints named `fk_newsletter_<child>_<parent>`). Implement `EnsureNewsletterTables`: for each table in parent-first order, `ExecContext(TableSchema(name))`, then one `ALTER TABLE … ADD COLUMN IF NOT EXISTS` block for every non-key column of that table (classifieds pattern). Wire it into `main.go` with `slog.Error("failed to create newsletter tables", …); os.Exit(1)`.
- [ ] **Step 4: Run** the tests again. Expected: PASS. Also run `go build ./... && go vet ./...`.
- [ ] **Step 5: Commit** `feat(newsletter): schema and EnsureNewsletterTables`.

### Task 2: Models and lists DB layer

**Files:**
- Create: `server/internal/models/newsletter.go`
- Create: `server/internal/database/newsletter_lists.go`
- Test: append to `server/internal/database/newsletter_integration_test.go`

**Interfaces:**
- Produces (models; JSON tags are snake_case versions of the field names):
  - Constants: `NewsletterStatusSubscribed = "subscribed"`, `NewsletterStatusUnsubscribed = "unsubscribed"`, `CampaignStatusDraft = "draft"`, `CampaignStatusScheduled = "scheduled"`, `CampaignStatusSent = "sent"`, `SubscriberSourcePublicForm = "public_form"`, `SubscriberSourceAdmin = "admin"`.
  - `NewsletterList{ID int64; Name, Description string; IsPublic bool; SubscribedCount int; CreatedAt *time.Time}`
  - `NewsletterSubscriber{ID int64; Email, Name, Status, Source string; ListIDs []int64; SubscribedAt, UnsubscribedAt, CreatedAt *time.Time}`. No token field.
  - `NewsletterCampaign{ID int64; Subject, PreviewText string; BodyHTML string \`json:"body_html,omitempty"\`; Status string; ListIDs []int64; ScheduledAt, SentAt *time.Time; RecipientCount *int; CreatedBy, UpdatedBy string; CreatedAt, UpdatedAt *time.Time}`
  - Requests:
    - `NewsletterSubscribeRequest{Email, Name string; Lists []int64; Website string}`
    - `NewsletterListCreateRequest{Name, Description string; IsPublic *bool}`
    - `NewsletterListPatchRequest{Name, Description *string; IsPublic *bool}`
    - `NewsletterSubscriberCreateRequest{Email, Name string; ListIDs []int64}`
    - `NewsletterSubscriberPatchRequest{Name, Status *string; ListIDs *[]int64}`
    - `NewsletterCampaignCreateRequest{Subject, PreviewText, BodyHTML string; ListIDs []int64}`
    - `NewsletterCampaignPatchRequest{Subject, PreviewText, BodyHTML, Status *string; ListIDs *[]int64}`
  - Responses:
    - `NewsletterSubscribeResponse{OK bool}`
    - `NewsletterListsResponse{Lists []NewsletterList}`
    - `NewsletterSubscribersResponse{Subscribers []NewsletterSubscriber; Pagination Pagination; Counts map[string]int}`
    - `NewsletterCampaignsResponse{Campaigns []NewsletterCampaign; Pagination Pagination; Counts map[string]int}`
    - `NewsletterRecipientCountResponse{Count int}`
    - `NewsletterStatsResponse{Subscribers map[string]int; Campaigns map[string]int; Lists []NewsletterList}`
- Produces (database):
  - `var ErrNewsletterDuplicate = errors.New("newsletter: duplicate")`, used for both list name and subscriber email.
  - `func ListNewsletterLists(ctx, conn) ([]models.NewsletterList, error)`: ordered by id; `SubscribedCount` counts members whose status is `subscribed`.
  - `func CreateNewsletterList(ctx, conn, name, description string, isPublic bool) (models.NewsletterList, error)`: returns `ErrNewsletterDuplicate` on MySQL error 1062.
  - `func UpdateNewsletterList(ctx, conn, id int64, req models.NewsletterListPatchRequest) (models.NewsletterList, error)`: `sql.ErrNoRows` if missing; `ErrNewsletterDuplicate` on 1062.
  - `func CountExistingNewsletterLists(ctx, conn, ids []int64, publicOnly bool) (int, error)`

- [ ] **Step 1: Write failing tests:**
  - `TestCreateNewsletterList_DuplicateName`: the second create with the same name returns `ErrNewsletterDuplicate` (`errors.Is`).
  - `TestListNewsletterLists_SubscribedCountExcludesUnsubscribed`: a list with 2 subscribed members and 1 unsubscribed has `SubscribedCount == 2` (seed with raw SQL).
  - `TestCountExistingNewsletterLists_PublicOnly`: lists A (public) and B (private); `[A,B]` with publicOnly → 1, without → 2; `[A, 999999]` → 1.
  - `TestUpdateNewsletterList_Partial`: patching only `IsPublic=false` leaves name and description unchanged.
- [ ] **Step 2: Run** them and expect FAIL (undefined).
- [ ] **Step 3: Implement.** Detect duplicates with `var me *mysql.MySQLError; errors.As(err,&me) && me.Number == 1062`. Re-read rows after writes (MaxScale `LastInsertId` caveat). Look the created row up by unique `name`.
- [ ] **Step 4: Run** them and expect PASS.
- [ ] **Step 5: Commit** `feat(newsletter): models and lists data layer`.

### Task 3: Subscribers DB layer

**Files:**
- Create: `server/internal/database/newsletter_subscribers.go`
- Test: append to `newsletter_integration_test.go`

**Interfaces:**
- Consumes: Task 2 models and `ErrNewsletterDuplicate`.
- Produces:
  - `func NewSubscriberToken() (string, error)`
  - `type PublicSubscription struct{ Email, Name string; ListIDs []int64; IP string }`. Email is already normalized by the caller.
  - `type SubscribeOutcome string`, with `SubscribeCreated`, `SubscribeUpdated` (was subscribed; lists unioned) and `SubscribeResubscribed`.
  - `func SubscribePublic(ctx, conn, p PublicSubscription) (SubscribeOutcome, error)`
  - `func CreateNewsletterSubscriber(ctx, conn, email, name string, listIDs []int64) (models.NewsletterSubscriber, error)`: `source=admin`; `ErrNewsletterDuplicate` if the email exists.
  - `func GetNewsletterSubscriber(ctx, conn, id int64) (models.NewsletterSubscriber, error)`: `sql.ErrNoRows` if missing; `ListIDs` populated.
  - `type SubscriberFilter struct{ Status string; ListID int64; Query string }`
  - `func ListNewsletterSubscribers(ctx, conn, f SubscriberFilter, limit, offset int) ([]models.NewsletterSubscriber, int, error)`: newest `subscribed_at` first; total count honours the filter.
  - `func CountNewsletterSubscribersByStatus(ctx, conn) (map[string]int, error)`: keys `subscribed`, `unsubscribed`, `all`, always present (zero when empty).
  - `func UpdateNewsletterSubscriber(ctx, conn, id int64, req models.NewsletterSubscriberPatchRequest) (models.NewsletterSubscriber, error)`
  - `func DeleteNewsletterSubscriber(ctx, conn, id int64) (bool, error)`

- [ ] **Step 1: Write failing tests** (each seeds lists via `CreateNewsletterList` and reads the token with raw `SELECT token`):
  - `TestSubscribePublic_NewAddress`: outcome `SubscribeCreated`; status `subscribed`; source `public_form`; signup_ip stored; token is 43 chars and matches `^[A-Za-z0-9_-]{43}$`; memberships equal the submitted list ids.
  - `TestSubscribePublic_AlreadySubscribedUnionsLists`: subscribe with [A], then [B]: outcome `SubscribeUpdated`; memberships {A,B}; token unchanged; one row.
  - `TestSubscribePublic_ResubscribeReplacesListsKeepsToken`: subscribe [A,B], unsubscribe via `UpdateNewsletterSubscriber`, subscribe [C] with name "New": outcome `SubscribeResubscribed`; memberships exactly {C}; name "New"; `unsubscribed_at` NULL; token equals the original.
  - `TestUnsubscribe_KeepsRowTokenAndMemberships`: after status→unsubscribed the row still exists, `unsubscribed_at` is set, the token is unchanged and the memberships are unchanged.
  - `TestSubscribePublic_CaseInsensitiveEmail` (Review Focus 1): the caller passes lowercased emails; assert that `SubscribePublic` with `"foo@example.com"` twice yields 1 row. Separately, inserting `Foo@Example.com` raw then subscribing `foo@example.com` hits the same row, because the column collation is case-insensitive (`utf8mb4_*_ci`), so no 500.
  - `TestSubscribePublic_ConcurrentSameEmail` (Review Focus 2): 10 goroutines → 0 errors, 1 row.
  - `TestCreateNewsletterSubscriber_Duplicate` → `ErrNewsletterDuplicate`.
  - `TestListSubscribers_Filters`: status filter, list filter and `q` matching name or email; `total` respects the filters.
  - `TestListSubscribers_QueryEscapesWildcards` (Review Focus 3): `q="a_b"` matches `a_b@x.io`, not `axb@x.io`.
  - `TestDeleteNewsletterSubscriber_RemovesMemberships`: returns true and the membership rows are gone; a second delete returns false.
- [ ] **Step 2: Run** them and expect FAIL.
- [ ] **Step 3: Implement.** `SubscribePublic` runs in one transaction:
  1. `INSERT INTO newsletter_subscribers (email,name,status,token,source,signup_ip,subscribed_at) VALUES (…) ON DUPLICATE KEY UPDATE id=id`.
  2. `SELECT id,status,name FROM … WHERE email=? FOR UPDATE`.
  3. Decide the outcome. Status `unsubscribed` → resubscribe: update status, `subscribed_at`, `unsubscribed_at=NULL`, source, IP and name, delete all memberships, then insert the new ones. Already subscribed and not inserted now → `INSERT IGNORE` the new memberships and update the name if one was given.

  Detect "inserted now" by comparing the stored token with the token generated for this call. Escape LIKE input by replacing `\` `%` `_` with backslash-escaped versions. Status changes in `UpdateNewsletterSubscriber` set `unsubscribed_at=NOW()` / `subscribed_at=NOW(), unsubscribed_at=NULL`. `ListIDs` replacement deletes and re-inserts the memberships in the same transaction.
- [ ] **Step 4: Run** them and expect PASS (run twice with `-count=2` to catch flakiness in the concurrency test).
- [ ] **Step 5: Commit** `feat(newsletter): subscriber data layer with state rules`.

### Task 4: Campaigns and stats DB layer

**Files:**
- Create: `server/internal/database/newsletter_campaigns.go`
- Test: append to `newsletter_integration_test.go`

**Interfaces:**
- Produces:
  - `var ErrCampaignSent = errors.New("newsletter: campaign already sent")`
  - `var ErrCampaignNotDraft = errors.New("newsletter: campaign is not a draft")`
  - `func CreateNewsletterCampaign(ctx, conn, req models.NewsletterCampaignCreateRequest, actor string) (models.NewsletterCampaign, error)`: always `draft`; `created_by=updated_by=actor`.
  - `func GetNewsletterCampaign(ctx, conn, id int64) (models.NewsletterCampaign, error)`: includes the body.
  - `func ListNewsletterCampaigns(ctx, conn, status, q string, limit, offset int) ([]models.NewsletterCampaign, int, error)`: `BodyHTML` is empty in list rows; newest `updated_at` first.
  - `func CountNewsletterCampaignsByStatus(ctx, conn) (map[string]int, error)`: keys `draft`, `scheduled`, `sent`, `all`, always present.
  - `func UpdateNewsletterCampaign(ctx, conn, id int64, req models.NewsletterCampaignPatchRequest, actor string) (models.NewsletterCampaign, error)`: ignores `req.Status` (the handler enforces it); returns `ErrCampaignSent` if the row's status is `sent` (checked under `FOR UPDATE`).
  - `func DeleteNewsletterCampaign(ctx, conn, id int64) (bool, error)`: returns `ErrCampaignNotDraft` for non-drafts.
  - `func NewsletterRecipientCount(ctx, conn, campaignID int64) (int, error)`: `COUNT(DISTINCT s.id)` over the join of campaign_lists → subscriber_lists → subscribers with `status='subscribed'`.
  - `func GetNewsletterStats(ctx, conn) (models.NewsletterStatsResponse, error)`

- [ ] **Step 1: Write failing tests:**
  - `TestCreateNewsletterCampaign_AlwaysDraft`: status `draft`, list ids round-trip, `created_by` stored.
  - `TestUpdateNewsletterCampaign_SentIsImmutable`: set status `sent` via raw SQL; update the subject → `ErrCampaignSent`; re-read and the subject is unchanged.
  - `TestDeleteNewsletterCampaign_OnlyDrafts`: a draft deletes and its memberships are gone; a raw-SQL `sent` row → `ErrCampaignNotDraft` and the row still exists.
  - `TestRecipientCount_DistinctAcrossListsExcludesUnsubscribed`: lists A and B; s1 in A+B, s2 in A, s3 in B (unsubscribed), s4 in no list; campaign targets A+B → 2.
  - `TestRecipientCount_IgnoresListPublicFlag` (Review Focus 4): make list A non-public → the count is unchanged.
  - `TestGetNewsletterStats`: the counts match seeded data; per-list subscribed counts match.
  - `TestListNewsletterCampaigns_OmitsBody`: `BodyHTML == ""` in list results; `Get` returns it.
- [ ] **Step 2: Run** them and expect FAIL.
- [ ] **Step 3: Implement.** Do updates in a transaction with `SELECT status … FOR UPDATE` before writing.
- [ ] **Step 4: Run** them and expect PASS.
- [ ] **Step 5: Commit** `feat(newsletter): campaign and stats data layer`.

### Task 5: Input validation (pure unit tests)

**Files:**
- Create: `server/internal/handlers/newsletter_validate.go`
- Test: `server/internal/handlers/newsletter_validate_test.go`

**Interfaces:**
- Produces:
  - `func normalizeNewsletterEmail(raw string) (string, bool)`: rules per the spec's handler step 3; returns the lowercased address.
  - `func normalizeNewsletterName(raw string) (string, bool)`: trims; empty is OK (`"", true`); ≤100 runes; no `unicode.IsControl`; `utf8.ValidString`.
  - `func normalizeListIDs(ids []int64, min, max int) ([]int64, bool)`: de-duplicates, sorts ascending, rejects ≤0, and checks `min ≤ len ≤ max` after de-duplication.
  - Constants: `maxNewsletterSubscribeBody = 4 << 10`, `maxCampaignBodyBytes = 1 << 20`, `maxSubjectLen = 255`, `maxPreviewLen = 255`.

- [ ] **Step 1: Write table tests.**
  - `TestNormalizeNewsletterEmail`. Accept:
    - `Foo@Example.com` → `foo@example.com`
    - `a.b+tag@sub.drexel.edu`

    Reject:
    - `""`
    - `"Bob <bob@x.io>"`
    - `"bob@x.io\r\nBcc: v@x.io"`
    - `" bob @x.io"`
    - `"bob@x"`
    - `"bob@@x.io"`
    - `"a@b@x.io"`
    - `"bob@.x.io"`
    - `"bob@x.io."`
    - `"bob@-x.io"`
    - `"(c)bob@x.io"`
    - a 65-char local part
    - a 255-byte address
    - `"bob@x.io\x00"`
    - `"bob@exаmple.com"` (Cyrillic а; must be rejected: domain must be ASCII)
  - `TestNormalizeNewsletterName`: OK on empty and `"  Ann  "` → `"Ann"`; reject 101 runes, `"a\x07b"` and invalid UTF-8 `"\xff"`.
  - `TestNormalizeListIDs`: `[3,1,3]` with (1,20) → `[1,3]`; `[]` → false; `[0]`, `[-1]` → false; 21 distinct → false; 21 entries that de-duplicate to 20 → true.
- [ ] **Step 2: Run** `go test ./internal/handlers/ -run Normalize -v` and expect FAIL.
- [ ] **Step 3: Implement.** Use `net/mail.ParseAddress` with `addr.Address == input` and `addr.Name == ""`. Then do manual checks: exactly one `@`, local ≤64, an ASCII-only domain with a dot and no leading/trailing `.` or `-` per label, and no space or control characters anywhere.
- [ ] **Step 4: Run** them and expect PASS.
- [ ] **Step 5: Commit** `feat(newsletter): hostile-input validation`.

### Task 6: `RateLimitGlobal` middleware

**Files:**
- Modify: `server/internal/middleware/rate_limit.go`
- Test: `server/internal/middleware/rate_limit_test.go`

**Interfaces:**
- Produces: `func RateLimitGlobal(limit int, window time.Duration) Middleware`, a single fixed window shared by all callers. Same 429 JSON body as `RateLimitByIP`. Add a package var `rateLimitNow = time.Now` used by both limiters so tests can move the clock.

- [ ] **Step 1: Write failing tests.**
  - `TestRateLimitGlobal_CapsAcrossIPs`: limit 3; requests from 3 different `RemoteAddr`s → 200; a 4th from a new IP → 429 with body `{"error":"rate limit exceeded"}`; the wrapped handler ran exactly 3 times.
  - `TestRateLimitGlobal_ResetsAfterWindow`: advance `rateLimitNow` past the window → 200 again.
- [ ] **Step 2: Run** them and expect FAIL.
- [ ] **Step 3: Implement** using a mutex-guarded `windowStart`/`count`, mirroring `RateLimitByIP`. Switch `RateLimitByIP`'s `time.Now()` to `rateLimitNow()`.
- [ ] **Step 4: Run** `go test ./internal/middleware/ -v` (all existing tests too) and expect PASS.
- [ ] **Step 5: Commit** `feat(middleware): global fixed-window rate limit`.

### Task 7: Public subscribe handler and route

**Files:**
- Create: `server/internal/handlers/newsletter.go`
- Modify: `server/internal/routes/routes.go`, `server/internal/routes/routes_test.go`
- Test: `server/internal/handlers/newsletter_integration_test.go`

**Interfaces:**
- Consumes: Tasks 3, 5 and 6, plus `akismet.Checker`.
- Produces: `func PostNewsletterSubscribe(conn *sql.DB, spamChecker akismet.Checker) http.Handler`. Route: `mux.Handle("POST /v1/newsletter/subscribe", middleware.RateLimitByIP(20, time.Hour)(middleware.RateLimitByIP(5, time.Minute)(middleware.RateLimitGlobal(300, 10*time.Minute)(handlers.PostNewsletterSubscribe(conn, spamChecker)))))`. Also produces the test helper `newsletterHandlerTestDB(t)`, which mirrors Task 1's helper inside the handlers package.

- [ ] **Step 1: Write failing tests** (fake checker: `type fakeSpamChecker struct{ spam bool; err error; calls int; last akismet.Comment }`):
  - `TestPostNewsletterSubscribe_IdenticalResponses`: for new, duplicate, honeypot (`website:"x"`) and spam (`fake{spam:true}`) submissions, every response has status 202 and the **same body bytes** `{"ok":true}\n`. Then check the DB: the new/duplicate address has 1 row; the honeypot and spam addresses have 0 rows.
  - `TestPostNewsletterSubscribe_AkismetErrorStillStores`: `fake{err: errors.New("down")}` → 202 and the row exists.
  - `TestPostNewsletterSubscribe_AkismetGetsSignup`: `last.Type == "signup"`; `AuthorEmail` is the normalized email; `UserIP` comes from `RemoteAddr`.
  - `TestPostNewsletterSubscribe_RejectsHostileInput`: table of bodies → each 400 with body `{"error":"invalid subscription"}`, and the subscriber table row count stays 0:
    - display-name email
    - non-public list id
    - unknown list id
    - `lists: []`
    - 21 lists
    - 5 KB body
    - `"lists":"1"` (wrong type)
    - non-JSON
  - `TestPostNewsletterSubscribe_NilCheckerStores`: a nil checker → stored.
  - `TestPostNewsletterSubscribe_DBErrorIsGeneric`: closed `*sql.DB` → 500 with body `{"error":"subscription failed"}` (no driver text).
- [ ] **Step 2: Add a routes test** in `routes_test.go`: `TestRegister_NewsletterSubscribeIsPublicAndRateLimited`. With the dead-DB setup, 5 POSTs with an invalid body from `RemoteAddr 203.0.113.9:1` → 400 (not 401); the 6th → 429.
- [ ] **Step 3: Run** them and expect FAIL.
- [ ] **Step 4: Implement** per the spec's handler steps 1–6. The Akismet call uses `Permalink: publicSiteURL+"/subscribe"` if a helper exists, else empty. Log the outcome with `slog.Info("newsletter subscribe", "outcome", …)` and **no email in logs**. Register the route.
- [ ] **Step 5: Run** them and expect PASS. Then `go vet ./...`.
- [ ] **Step 6: Commit** `feat(newsletter): public subscribe endpoint`.

### Task 8: Admin handlers, routes, Swagger

**Files:**
- Create: `server/internal/handlers/newsletter_admin.go`
- Modify: `server/internal/routes/routes.go`, `server/internal/routes/routes_test.go`, `server/main.go` (`@tag.name newsletter`), `server/docs/*` (generated)
- Test: `server/internal/handlers/newsletter_admin_integration_test.go`

**Interfaces:**
- Consumes: Tasks 2–5.
- Produces handlers (all `func X(conn *sql.DB) http.Handler`) and routes exactly as in the spec's Authenticated table:
  - `GetNewsletterStats`
  - `GetNewsletterLists`, `PostNewsletterList`, `PatchNewsletterList`
  - `GetNewsletterSubscribers`, `PostNewsletterSubscriber`, `PatchNewsletterSubscriber`, `DeleteNewsletterSubscriber`
  - `GetNewsletterCampaigns`, `GetNewsletterCampaign`, `PostNewsletterCampaign`, `PatchNewsletterCampaign`, `DeleteNewsletterCampaign`
  - `GetNewsletterRecipientCount`

  Literal segments (`/recipients/count`) sit below `{id}`, so there's no mux conflict. The actor is `middleware.UserFromContext(r.Context()).Email`, else `"unknown"`. Each mutation calls `activity.LogRequest(r, "newsletter_<noun>_<verb>", …)` without email addresses in the message.

  HTTP mapping:

  | Condition | Response |
  |---|---|
  | `sql.ErrNoRows` | 404 |
  | `ErrNewsletterDuplicate` | 409 |
  | `ErrCampaignSent`, `ErrCampaignNotDraft` | 409 |
  | `status` ∉ {"", "draft"} on campaign patch | 409 `sending isn't available yet`, checked **before** any write |
  | validation | 400 with a specific message (admin routes may be specific) |
  | unknown list ids in `list_ids` (`CountExistingNewsletterLists(…, false)` mismatch) | 400 |
  | campaign body > `maxCampaignBodyBytes` | 400 (`MaxBytesReader` 1 MB + 4 KB) |
  | subscriber PATCH status ∉ {subscribed, unsubscribed} | 400 |

- [ ] **Step 1: Write failing tests.** Call handlers directly with `middleware.ContextWithUser`, using editor and admin users. Role-gating tests go through `routes.Register`-style wrapping with `middleware.RequireAdmin`.
  - `TestPatchNewsletterCampaign_ScheduleBlocked`: `{"status":"scheduled"}` → 409; re-read and the status is still `draft`.
  - `TestPatchNewsletterCampaign_SentConflict`: a raw-SQL `sent` row, patch the subject → 409; the subject is unchanged.
  - `TestPostNewsletterCampaign_UnknownList`: → 400 and no campaign row.
  - `TestNewsletterSubscriberDelete_EditorForbidden`: `RequireAdmin(DeleteNewsletterSubscriber)` with an editor → 403 and the row still exists; with an admin → 204 and the row is gone.
  - `TestPatchNewsletterSubscriber_Unsubscribe`: → 200; the JSON has `status:"unsubscribed"`, a non-null `unsubscribed_at` and no `token` key anywhere in the body (`!strings.Contains(body, "token")`).
  - `TestGetNewsletterSubscribers_NeverLeaksToken`: same assertion on the list response.
  - `TestPostNewsletterSubscriber_Duplicate`: → 409.
  - `TestGetNewsletterRecipientCount`: matches a seeded scenario (2).
  - `TestGetNewsletterStats_Shape`: keys present with zero values on an empty DB.
- [ ] **Step 2: Routes test** `TestRegister_NewsletterAdminRoutesGated`: every admin path/method in the spec's table → 401 without a session.
- [ ] **Step 3: Run** them and expect FAIL. **Step 4: Implement** and register the routes. **Step 5: Run** them and expect PASS.
- [ ] **Step 6: Swagger.** Run `cd server && swag init --parseDependency --parseInternal`, then `git diff --stat server/docs` shows the newsletter paths. Then `go build ./... && go vet ./... && go test -race ./...`.
- [ ] **Step 7: Commit** `feat(newsletter): admin API and swagger`.

### Task 9: Dashboard page

**Files:**
- Create: `frontend/src/lib/newsletterApi.ts` (types mirroring the Go models plus fetch helpers)
- Rewrite: `frontend/src/pages/newsletterView.tsx`
- Create: `frontend/src/components/newsletter/CampaignFormDialog.tsx`, `SendCampaignDialog.tsx`, `SubscribersTab.tsx`, `ListsPanel.tsx`
- Modify: `frontend/src/App.tsx` (re-add the lazy import and `/newsletter` route), `frontend/src/components/Sidebar.tsx` (uncomment the `Mail` import and the entry; delete the "temporarily disabled" comment)
- Test: `frontend/src/pages/newsletterView.test.tsx`

**Interfaces:**
- `newsletterApi.ts` exports:
  - types: `NewsletterList`, `NewsletterSubscriber`, `NewsletterCampaign`, `NewsletterStats`, `Paginated<T>`
  - functions taking `apiFetch: ReturnType<typeof useApiFetch>` and throwing `Error(body.error)` on non-2xx:
    - `getStats`, `getLists`, `createList`, `patchList`
    - `getSubscribers(params)`, `createSubscriber`, `patchSubscriber`, `deleteSubscriber`
    - `getCampaigns(params)`, `getCampaign`, `createCampaign`, `patchCampaign`, `deleteCampaign`
    - `getRecipientCount`
- Admin-only controls use `useCurrentUserRole().isAdmin`. Deletes use `window.confirm` (classifieds pattern). Dialogs are `role="dialog"` with `aria-modal` and a labelled heading (MediaPicker pattern).
- Tiles: exactly `Subscribed`, `Unsubscribed`, `Drafts`, `Sent`.
- `SendCampaignDialog` props: `{ campaign: NewsletterCampaign; lists: NewsletterList[]; onClose(): void }`. On open it calls `getRecipientCount`. Text: `Would reach {n} subscribers in {list names joined ", "}.` A disabled button labelled `Send` with an adjacent note `Delivery isn't built yet.`

- [ ] **Step 1: Write failing Vitest tests** (`vi.mock("../hooks/useApiFetch")` and `vi.mock("../hooks/useCurrentUserRole")` with a stub router like `editArticleView.test.tsx`; record calls):
  - `renders stat tiles from the API and no open-rate`: the stats stub `{subscribers:{subscribed:1234,unsubscribed:56,all:1290},campaigns:{draft:2,scheduled:0,sent:0,all:2},lists:[…]}` → shows `1,234`, `56`, `2`, `0`; `queryByText(/open rate/i)` is null; none of the old mock addresses (`jsmith@drexel.edu`) appear.
  - `send dialog shows recipient count and cannot send`: click Send on a draft → shows `Would reach 1,234 subscribers in Drexel Students, Alumni.`; the dialog's Send button `toBeDisabled()`; after clicking it no request has method POST/PATCH and nothing hit any `/send` URL.
  - `creating a campaign posts the form`: fill in the subject/body, tick list "Alumni", save → exactly one POST to `/v1/newsletter/campaigns` with body `{subject, preview_text:"", body_html, list_ids:[2]}`.
  - `unsubscribe toggles via PATCH and updates the row`: Subscribers tab → click Unsubscribe → PATCH `/v1/newsletter/subscribers/5` body `{status:"unsubscribed"}`; the row shows `Unsubscribed`.
  - `editor sees no delete controls`: with `isAdmin:false` there are no `Delete` buttons on the subscriber rows or draft campaigns; with an admin they are present.
  - `api error shows error state`: stats 500 `{error:"boom"}` → an alert with `boom`; no table rows.
  - `renders empty states with no lists` (Review Focus 5): empty everything → `No campaigns yet`, `No subscribers yet`; the New campaign button is disabled with the hint `Create a list first`.
- [ ] **Step 2: Run** `cd frontend && NODE_ENV=test npx vitest run src/pages/newsletterView.test.tsx`. Expected: FAIL.
- [ ] **Step 3: Implement** the API module, page and components. Delete the `CAMPAIGNS`/`SUBSCRIBERS` constants entirely. Subscriber search is server-side (`q`, debounced 300 ms). The campaign body uses a plain `<textarea>`; the editor gets scaffolded later.
- [ ] **Step 4: Run** the tests and expect PASS. Then `npm run lint` (0 errors), `npm run build`, and `NODE_ENV=test npm test`.
- [ ] **Step 5: UI check.** Load the `ui-check` skill. Run the local stack (`docker start` the containers, `go run ./main.go`, `npm run dev -- --port 5173`) and view `/newsletter` signed in: tiles, both tabs, the send dialog, and the empty state.
- [ ] **Step 6: Commit** `feat(newsletter): real dashboard page, restore sidebar entry`.

### Task 10: Ship

- [ ] **Step 1: Full verification.**
  - `cd server && go test -race ./... && go vet ./... && CMS_TEST_DSN=… go test ./internal/... -p 1`
  - swag drift is clean (`git status server/docs` shows nothing after re-running `swag init`)
  - frontend lint, build and `NODE_ENV=test npm test` pass
  - `docker compose -f deploy/compose.cms.yml config -q`
- [ ] **Step 2: Rebase** onto `origin/main` (PR #245 may have merged; resolve `App.tsx` by keeping the route). Push `feat/newsletter` and open one PR to `main` with a summary, the decisions table from the spec, and a test list. Do not merge.
- [ ] **Step 3: Handoff.** Add a Notion comment on the backend card (done / left / decisions / PR links), and add the `NODE_ENV=test` note to AGENTS.md.
