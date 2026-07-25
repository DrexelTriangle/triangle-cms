# CI/CD Workflows

`ci.yml` runs on pull requests and pushes without production secrets. It covers
backend tests, race tests, `go vet`, frontend lint/build, Docker image builds,
and Compose validation.

`publish.yml` runs only from a successful `CI` workflow run on `main` that was
triggered by a trusted push. It validates
`github.event.workflow_run.head_sha` as a 40-character hexadecimal SHA and
publishes backend and frontend GHCR images tagged only with that full SHA.

`deploy.yml` runs on the narrowly labelled self-hosted runner inside the Drexel
VPN. Automatic deployments use the trusted publish run `head_sha`; manual
deployments accept an already-published image SHA as data only. Deployment code
is always checked out from the protected default branch, never from the supplied
image SHA.
