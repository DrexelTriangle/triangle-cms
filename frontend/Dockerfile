FROM node:22-alpine AS builder
WORKDIR /app

# Baked in at build time (Vite inlines import.meta.env), so this is the origin
# every "View Live" link and permalink preview in the published image points at.
# Override with --build-arg to publish an image aimed at another site.
ARG VITE_PUBLIC_SITE_URL="https://www.thetriangle.org"
ENV VITE_PUBLIC_SITE_URL=${VITE_PUBLIC_SITE_URL}
# "live" points the Newsletter screens at /v1/newsletters; empty keeps them on
# the in-browser mock. Published images are live.
ARG VITE_NEWSLETTER_API="live"
ENV VITE_NEWSLETTER_API=${VITE_NEWSLETTER_API}

COPY package.json package-lock.json .npmrc ./
RUN npm ci --include=dev --no-audit --no-fund

COPY . .
RUN npm run build

FROM nginx:1.30-alpine
# Pull in security fixes newer than the base image (e.g. musl CVE-2026-107891).
RUN apk upgrade --no-cache
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 8080
HEALTHCHECK --interval=20s --timeout=5s --retries=5 --start-period=10s \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
