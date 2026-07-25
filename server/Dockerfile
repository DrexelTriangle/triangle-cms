FROM golang:1.25-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download
# Pin swag to the version declared in go.mod so doc generation is reproducible
# (an unpinned @latest silently changes the build over time).
RUN go install github.com/swaggo/swag/cmd/swag@v1.16.6

COPY . .
RUN swag init --parseDependency --parseInternal
# TARGETARCH is provided automatically by Buildx for the target platform; fall
# back to amd64 for a plain `docker build` where it is unset.
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build -o /bin/cms ./main.go

FROM alpine:3.21
WORKDIR /app

# ca-certificates: the CMS makes outbound HTTPS calls at startup (OIDC provider
# discovery against Authentik). The base alpine image ships no root CA bundle, so
# without this the TLS handshake fails with "x509: certificate signed by unknown
# authority" and the process exits. tzdata is included so time zone conversions
# resolve against real zone data rather than UTC-only fallbacks.
RUN apk add --no-cache ca-certificates tzdata

# Run as an unprivileged user. The process needs no write access at runtime
# (activity/audit state now lives in MariaDB, not on local disk) and binds only
# the unprivileged port 8080, so root is unnecessary. TLS key/cert files mounted
# at runtime must be readable by this UID (10001).
RUN addgroup -S app && adduser -S -G app -u 10001 app

COPY --from=builder --chown=app:app /bin/cms /app/cms

# TLS certificates are NOT baked into the image. They are environment-specific
# secrets and are provided at runtime (bind mount / Nginx termination). See
# docker-compose.yml and scripts/generate_certs.sh.

USER app

EXPOSE 8080
ENTRYPOINT ["/app/cms"]
