# CI/CD Workflows

`ci.yml` runs on pull requests and pushes without production secrets. It covers
backend tests, race tests, `go vet`, frontend lint/build, Docker image builds,
Compose validation, and the deployment script test suite
(`deploy/scripts/deploy_scripts_test.sh`). That suite stubs `docker`, `curl`, and
`nginx` on `PATH`, so it needs no daemon or privileges — it exercises slot
selection, the transactional Nginx switch and its restore-on-failure paths, and
the preflight checks.

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
concurrency group with `deploy.yml` so a rollback can never interleave with a
deployment. `slot: auto` targets whichever slot is currently inactive; `blue` or
`green` names one explicitly. To recover an *older* image SHA instead, run
`deploy.yml` manually with that SHA — rollback only moves traffic between the two
slots that are already up.
