# CI/CD Workflows

`ci.yml` runs on pull requests and pushes without production secrets. It covers
backend tests, race tests, `go vet`, frontend lint/build, Docker image builds,
Compose validation, and the deployment script test suite
(`deploy/scripts/deploy_scripts_test.sh`). That suite stubs `docker`, `curl` and
`nginx` on `PATH`, so it needs no daemon or privileges: it exercises slot
selection, the transactional Nginx switch and its restore-on-failure paths, and
the preflight checks.

`ci.yml`'s `swagger-docs` job regenerates `server/docs` with the pinned `swag`
version from `server/Dockerfile` and fails on any diff. The image build already
regenerates the spec, so the binary is always current; this guards the
*committed* copy, which is what `pages.yml` publishes.

`publish.yml` runs only from a successful `CI` workflow run on `main` that was
triggered by a trusted push. It validates
`github.event.workflow_run.head_sha` as a 40-character hexadecimal SHA and
publishes backend and frontend GHCR images tagged only with that full SHA.

`deploy.yml` runs on the narrowly labelled self-hosted runner inside the Drexel
VPN. Automatic deployments use the trusted publish run `head_sha`; manual
deployments accept an already-published image SHA as data only. Deployment code
is always checked out from the protected default branch, never from the supplied
image SHA.

`rollback.yml` is manual (`workflow_dispatch`) and switches Nginx back to the
other slot, which is already running the previous release. It pulls no images, so
it holds no `packages` permission. It shares the `delta-production-deploy`
concurrency group with `deploy.yml`, so a rollback can never interleave with a
deployment. `slot: auto` targets whichever slot is currently inactive; `blue` or
`green` names one explicitly. Rollback only moves traffic between the two slots
already up; to recover an *older* image SHA, run `deploy.yml` manually with it.

`pages.yml` publishes the committed Swagger spec as a static Swagger UI site on
GitHub Pages (<https://drexeltriangle.github.io/triangle-cms/>). It is independent
of the CI -> Publish -> Deploy chain: no `packages` permission, no slot, never on
the self-hosted runner. Swagger UI's assets are vendored
into the artifact at build time from a pinned `swagger-ui-dist`, so the published
page loads nothing from a third-party CDN at runtime.

## Scheduling a feature release

`scheduled-merge.yml` merges a pull request at a time you pick, with nobody
present to click Merge. Merging `main` is what starts the deploy chain, so the
scheduled merge time is the publish time plus one deploy.

To schedule a release:

1. Open the pull request as normal and let CI go green.
2. Put a `Merge-at:` line anywhere in the pull request **description**:

   ```
   Merge-at: 2026-09-12 00:01 America/New_York
   ```

   Either a wall-clock time plus an [IANA zone][tz] (correct across daylight
   saving, and the form to prefer) or an absolute instant
   (`2026-09-12T00:01:00-04:00`, `2026-09-12T04:01:00Z`). A wall-clock time the
   spring-forward jump skips over is rejected rather than guessed at.
3. Add the `scheduled-merge` label. That label is the arming switch: no label,
   no automatic merge.

Optionally add `Merge-method: merge|squash|rebase` to the description. The
default is `squash`.

### What it checks before merging

The workflow wakes every ten minutes and, for each labelled pull request whose
`Merge-at:` has passed, refuses to merge unless the pull request is open, not a
draft, conflict-free, unblocked by branch protection, and every check run and
commit status on its head commit has **finished and passed**. A pull request
with checks still running is left alone and reconsidered on the next tick; one
with no checks at all is refused outright, so nothing unvalidated ships.

If something is actually wrong (failed checks, conflicts, still a draft), the
workflow comments with the reason, swaps the `scheduled-merge` label for
`scheduled-merge-blocked`, and stops. Dropping the label is deliberate: it
means one explanatory comment instead of one every ten minutes, and it means a
broken release never merges later "by surprise" once the problem clears. Fix the
problem and re-add the label to re-arm it.

### Timing accuracy

GitHub runs scheduled workflows on a best-effort queue and frequently several
minutes late, occasionally dropping ticks entirely under load. Read `Merge-at:`
as **not before** that time — in practice it lands within about fifteen minutes
after. Don't schedule anything that has to be exact to the minute; for a
genuinely hard deadline, merge by hand.

`workflow_dispatch` runs the same pass immediately, which is the way to test a
setup or push a release out early. Its `pr` input narrows the run to one pull
request and `dry_run` reports what would happen without merging anything.

### Required setup: the release App

This is the one piece that cannot live in the repository. The workflow merges as
a **GitHub App** and fails loudly if the App credentials are missing, rather
than merging without them. Two separate constraints force an App here, and
neither has a workaround:

**It cannot use the built-in `GITHUB_TOKEN`.** A push made with `GITHUB_TOKEN`
does not trigger further workflows. The deploy chain hangs off a `workflow_run`
from a *push* to `main`, so a `GITHUB_TOKEN` merge would land the commit and
then deploy nothing at all — the one failure mode a timed release must not have.

**It cannot use a personal access token either.** `main` requires an approving
review, and a scheduled release merges without one, so the merging identity has
to be a ruleset bypass actor. Rulesets can name a GitHub App as a bypass actor
(`actor_type: Integration`) and nothing else of comparable narrowness — a PAT
bypasses only by belonging to an organisation admin, which means leaving an
org-admin-grade credential in a repository secret to merge one pull request.

To set it up:

1. Create a GitHub App in the organisation (Settings → Developer settings →
   GitHub Apps → New). It needs no webhook and no user-facing callback.
2. Give it these repository permissions, and no others:

   | Permission | Level | Why |
   | --- | --- | --- |
   | Contents | Read and write | performs the merge |
   | Pull requests | Read and write | reads the PR, comments, moves labels |
   | Checks | Read-only | confirms check runs passed |
   | Commit statuses | Read-only | confirms commit statuses passed |
   | Metadata | Read-only | mandatory for any App |

3. Install it on this repository only.
4. Generate a private key and store the App's numeric ID and the `.pem`
   contents as the repository secrets `RELEASE_BOT_APP_ID` and
   `RELEASE_BOT_PRIVATE_KEY`.
5. Add the App to the branch ruleset for `main` as a bypass actor. Without this
   step everything else works and the merge is still refused by the review rule.

The workflow mints a short-lived installation token per run with
`actions/create-github-app-token`, so nothing long-lived sits in the secret
except the private key, and the merge is attributable to the App rather than to
a person.

### The deploy gate

Merging is only half of an unattended release. The `production` environment has
required reviewers, so a deployment stops and waits for a human even after the
merge has landed — which would leave a midnight release merged but unpublished
until someone woke up and clicked Approve.

Environments have no bypass-actor concept, so unlike the branch ruleset there is
no way to exempt the release App from the reviewer gate. Instead the deploy
workflow picks its environment from who pushed the commit:

```yaml
environment:
  name: ${{ github.event.workflow_run.actor.login == 'tri-release-bot[bot]'
        && 'production-auto' || 'production' }}
```

`production-auto` carries the same deployment branch policy as `production` but
no required reviewers. Only the release App can push a scheduled merge, and only
the scheduled-merge workflow holds its key, so that environment is reachable
only by a pull request that carried the `scheduled-merge` label. Everything a
person merges still lands on `production` and still waits for a reviewer, and a
manual `workflow_dispatch` has no `workflow_run` actor at all, so it also falls
through to the gated environment.

Two things to keep in mind:

- **The two environments must stay in step.** Any variable, secret or branch
  policy added to `production` has to be added to `production-auto` as well, or
  scheduled releases will deploy with different configuration from manual ones.
  In `triangle-cms` that currently means the three `DELTA_*` variables.
- **`triangle-cms` chains through two `workflow_run` hops** (CI → Publish Images
  → Deploy Delta) where Scalene chains through one. This relies on the actor
  propagating across both hops; if a scheduled CMS release ever stops at the
  reviewer gate, that assumption is where to look first.

### Why `mergeable_state` is not used as a gate

GitHub reports `mergeable_state: "blocked"` for any pull request missing a
required approval, and that field is **not actor-aware** — it describes the
state for an ordinary merger and knows nothing about bypass actors. Refusing on
it would cancel every scheduled release, so the workflow only logs it and lets
the merge proceed. The safety properties come from checks this workflow verifies
itself (open, non-draft, conflict-free, every check finished and passing, at
least one check present), and GitHub stays the final authority: if it genuinely
refuses the merge, the error is reported rather than guessed at.

What this does mean is that a scheduled release **skips code review by design**.
The `scheduled-merge` label is the thing authorising that, so treat adding it as
the approval.

[tz]: https://en.wikipedia.org/wiki/List_of_tz_database_time_zones
