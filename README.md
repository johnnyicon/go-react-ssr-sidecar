# go-react-ssr-sidecar

A production-tested template for running a React 18 SPA with server-side rendering via a Node.js sidecar process, all served from a single Go binary using `go:embed`. No Next.js, no complex build toolchain — just Go, Vite, and TanStack Router.

## What this pattern is

The web server is a Go binary that embeds the compiled React frontend (`go:embed`). For SSR-eligible routes (public content pages), Go spawns a Node.js process at startup that imports the Vite-built SSR bundle. When a request arrives, Go sends the URL and initial page data to the sidecar over localhost HTTP, receives the rendered HTML string, injects it into the embedded `index.html`, and serves the complete document. React then hydrates the server-rendered DOM on the client.

For everything else (authenticated pages, unknown routes), Go serves bare `index.html` and React does a normal client-side render. The Node sidecar has a circuit breaker: if it's down or slow, Go falls back to CSR automatically.

## Why not Next.js

The Go API stays. The business logic is in Go, the database queries are in Go, the session management is in Go. Migrating to Next.js would mean rewriting the API in Node or adding a second runtime layer. Instead, this pattern keeps Go as the sole backend runtime — Node is just a rendering tool, not a server.

The result: one binary to deploy, one port to expose, no Node.js server in the hot path, full control over caching and request routing.

## Architecture

```
Browser
  │
  ▼
[Go binary :3000]
  │
  ├─ /api/*  ──────────────────────────► [Go API server :8080]
  │                                            │
  │  (proxy; ModifyResponse reads              │  X-App-Invalidate header
  │   X-App-Invalidate header)                 │  triggers HTML cache eviction
  │                                            ▼
  ├─ /content/:id/:slug                  [PostgreSQL]
  │     │
  │     ├─ cache hit? → serve cached HTML (sync.Map, TTL 10min, SWR 30min)
  │     │
  │     └─ cache miss:
  │           │
  │           ├─ DB query (cover image URL, initial data)
  │           │
  │           ├─ POST http://localhost:3010/render ─► [Node SSR sidecar]
  │           │         { url, coverImage, initialData }    │
  │           │         ◄──────────────── HTML string ──────┘
  │           │
  │           ├─ inject HTML into <div id="root">
  │           ├─ inject <link rel="preload" as="image"> (LCP optimization)
  │           ├─ inject <script>window.__APP_DATA__={...}</script>
  │           └─ serve + cache
  │
  └─ /* ──── go:embed static/dist/ ──── index.html (SPA catch-all)

[Node SSR sidecar :3010]
  ├─ imports dist-ssr/entry-server.js (Vite SSR bundle, no node_modules)
  └─ POST /render → render(url, coverImage, initialData) → HTML string
```

## Quick start

**Prerequisites:** Go 1.23+, Node 20+

```bash
# 1. Clone and install frontend dependencies
git clone https://github.com/johnnyicon/go-react-ssr-sidecar
cd go-react-ssr-sidecar
cd frontend && npm install && cd ..

# 2. Build the React SPA + SSR bundle
cd frontend && npm run build && npm run build:ssr && cd ..

# 3. Copy the built frontend into the go:embed path
cp -r frontend/dist static/dist/

# 4. Download Go dependencies and build
go mod tidy
go build ./...

# 5. Run the web server (starts Node sidecar automatically)
go run ./cmd/web
# → web: listening on :3000
# → SSR: started (pid XXXX, ...)
# → SSR: server ready
```

Visit http://localhost:3000. SSR is active.

## What to customize vs. what to keep

| What | File | Keep or customize |
|---|---|---|
| SSR trigger routes | `cmd/web/main.go` | **Customize**: add your routes, remove the placeholder |
| `initialData` struct | `cmd/web/main.go` | **Customize**: define your data types |
| `criticalCSS` constant | `cmd/web/main.go` | **Customize**: extract from your app (see docs/03) |
| Window global name (`__APP_DATA__`) | `main.go` + `main.tsx` + `SSRContext.tsx` | Keep as-is unless you have a naming reason |
| Circuit breaker timing (30s reset) | `cmd/web/main.go` | Keep unless you have a very slow SSR process |
| Cache TTL values | `cmd/web/main.go` | **Customize** for your content freshness needs |
| `SSRContext.tsx` interface | `frontend/src/contexts/` | **Customize**: replace `unknown` with your type |
| Router routes | `frontend/src/router.ts` | **Customize**: add your real routes |
| `ssr-server.mjs` | `frontend/` | Keep as-is |
| `entry-server.tsx` | `frontend/src/` | Keep as-is (or add route loaders) |
| Vite configs | `frontend/vite.config*.ts` | Keep structure, customize plugins |
| `Dockerfile.web` | root | Keep structure, customize env vars |
| Railway configs | `railway.*.toml` | **Customize**: update watchPatterns |

## For AI coding agents

Read `AGENT-GUIDE.md` first. It explains the full request lifecycle, the 6 required customization points with exact file locations, common mistakes and how to detect them, and a testing checklist.
