# web

The observer dashboard: a static Next.js export that reads the observer API
in the browser. Three data views (network overview, validator detail, blob
detail) plus methodology, about and API pages.

```
npm ci
NEXT_PUBLIC_API_BASE=http://127.0.0.1:8080 npm run dev     # against a local observer-api
npm run build                                               # writes out/ for Caddy
```

`NEXT_PUBLIC_API_BASE` defaults to same-origin `/api`, which is what the
Caddyfile in `deploy/` proxies to `observer-api`.

Design tokens, badge set and layout follow
`docs/research/R9-presentation-and-celestia-design.md`; verdict arithmetic
follows `docs/verdicts.md`.
