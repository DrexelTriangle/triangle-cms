# Triangle CMS dashboard

React + TypeScript + Vite. The editor dashboard: articles, media, taxonomy,
polls, classifieds, settings.

Setup and running it against a local backend are in the
[repo README](../README.md).

```bash
npm run dev -- --port 5173   # dev server (strict port 5173)
npm run build                # tsc -b + vite build
npm run lint
npm test                     # vitest
```

API calls go through `useApiFetch`, which prefixes `VITE_API_BASE_URL`. That is
empty by default, so requests are same-origin and the dev server proxies `/v1`
to `https://localhost:8080` with `secure: false`, which is what lets the local
backend keep its self-signed certificate. `VITE_AUTH_BASE_URL` and
`VITE_PUBLIC_SITE_URL` are the other two overrides (see `src/auth/urls.ts`).

For a UI preview without Authentik, set `VITE_DEV_AUTH_BYPASS=true` in
`.env.development.local`. This only bypasses frontend sign-in in the dev server
on localhost; API requests still require a backend and its normal authentication.
