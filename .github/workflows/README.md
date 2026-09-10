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

### Required setup: `RELEASE_BOT_TOKEN`

This is the one piece that cannot live in the repository. The workflow merges
with a repository secret named `RELEASE_BOT_TOKEN` and **fails loudly if it is
missing** rather than merging without it.

The reason is a deliberate GitHub rule: a push made with the built-in
`GITHUB_TOKEN` does not trigger further workflows. The deploy chain hangs off a
`workflow_run` from a *push* to `main`, so a `GITHUB_TOKEN` merge would land the
commit and then deploy nothing at all — which is the one failure a timed release
must never have. A user or GitHub App token produces a real push event, so the
chain runs exactly as if a person had clicked Merge.

Create a fine-grained personal access token (or a GitHub App installation token)
scoped to this repository with **Contents: read and write** and **Pull requests:
read and write**, and save it as the `RELEASE_BOT_TOKEN` repository secret.
Whoever owns the token appears as the merge author, so prefer a machine account
over a person where one is available. Note that a fine-grained PAT expires:
put its expiry somewhere you will see it, because the failure mode is a release
that silently does not go out.

[tz]: https://en.wikipedia.org/wiki/List_of_tz_database_time_zones
