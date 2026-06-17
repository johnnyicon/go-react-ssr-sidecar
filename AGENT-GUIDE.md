# AGENT-GUIDE: go-react-ssr-sidecar

This guide is written for AI coding agents. It covers everything you need to understand this codebase, adapt it correctly, and avoid the common mistakes that produce subtle broken behavior.

---

## 1. What this repo is

A production-tested template for serving a React 18 SPA with server-side rendering, shipped as a single Go binary. Extracted from the SowOneGoodSeed platform (Go + Echo + TanStack Router, deployed on Railway, ~10k monthly active users).

**The problem it solves:** React SPAs have terrible LCP (Largest Contentful Paint) on mobile. Without SSR, the browser downloads JavaScript, parses it, executes it, then React fetches data from an API, then renders the UI. On mobile 3G, this takes 4-8 seconds before the user sees meaningful content. Google's Core Web Vitals penalizes this.

**The solution:** Server-render the initial page HTML in Go (using a Node.js sidecar) so the user sees real content in ~500ms, then React hydrates and takes over for client-side navigation.

**Why not Next.js:** The API and business logic live in Go. This pattern keeps Go as the sole backend — Node.js is just a rendering tool invoked per-request, not a server framework.

---

## 2. How it works: full request lifecycle

```
Browser GET /content/abc123/my-post
         │
         ▼
[Go web server, cmd/web/main.go]
         │
         ├─ 1. Check route: is this an SSR-eligible route?
         │     If not (auth pages, CSR-only): serve indexHTML directly → done
         │
         ├─ 2. Check in-memory HTML cache (sync.Map)
         │     Cache hit + fresh (< 10min):  serve cached HTML → done
         │     Cache hit + stale (10-40min): serve cached HTML AND kick off background refresh
         │     Cache miss: continue to step 3
         │
         ├─ 3. Query database for content data
         │     (cover image URL, title, body, etc.)
         │
         ├─ 4. Build initialData struct (Go) — this is the Go↔React data bridge
         │
         ├─ 5. callSSR(path, coverImage, initialData)
         │        │
         │        ├─ circuit breaker open? → return error → go to step 7
         │        │
         │        └─ POST http://localhost:3010/render
         │              body: { url, coverImage, initialData }
         │              timeout: 1500ms
         │                        │
         │              [Node SSR sidecar, ssr-server.mjs]
         │                        │
         │                        ├─ import dist-ssr/entry-server.js
         │                        ├─ call render(url, coverImage, initialData)
         │                        │     ├─ createMemoryHistory({ initialEntries: [url] })
         │                        │     ├─ createAppRouter({ history: memHistory })
         │                        │     ├─ await router.load() (route matching + loaders)
         │                        │     └─ renderToString(<SSRProvider>...</SSRProvider>)
         │                        └─ return HTML string
         │
         ├─ 6. On SSR success:
         │     injectSSRContent(indexHTML, ssrHTML, coverImage, initialData)
         │       ├─ inject <link rel="preload" as="image"> for cover (LCP)
         │       ├─ inject <script>window.__APP_DATA__={coverImage, initialData}</script>
         │       └─ replace <div id="root"></div> with <div id="root">{ssrHTML}</div>
         │     store in cache (setStaticHTML)
         │     serve HTML → done
         │
         └─ 7. On SSR failure (circuit breaker, timeout, error):
               injectCoverPreload(indexHTML, coverImage)  ← lighter optimization
               store in cache
               serve HTML (CSR fallback) → done

Browser receives HTML with pre-rendered content
         │
         ▼
React bundle downloads (from /assets/*, immutable-cached)
         │
         ▼
main.tsx executes:
  1. Read window.__APP_DATA__ → { coverImage, initialData }
  2. Wrap <App> in <SSRProvider value={{ coverImage, initialData }}>
  3. root.hasChildNodes() ? hydrateRoot(root, app) : createRoot(root).render(app)
         │
         ▼
hydrateRoot: React walks the server-rendered DOM and attaches event listeners
             WITHOUT re-rendering (fast path)
             — requires that SSRProvider props match exactly what entry-server.tsx used
         │
         ▼
App is interactive. Client-side navigation takes over.
```

---

## 3. The 6 things you MUST customize per project

### 3.1 SSR trigger routes

**File:** `cmd/web/main.go`
**What:** Which URL patterns call `callSSR()` vs. serve bare `indexHTML`

The template has a placeholder `/content/:id/:slug` route. Replace it with your actual routes.

Rules:
- SSR only public, unauthenticated content pages
- NEVER SSR authenticated routes (dashboard, settings, profile)
- Skip SSR for "reserved slugs" that route to edit/manage pages

```go
// Add your SSR routes
e.GET("/posts/:id/:slug", func(c echo.Context) error {
    // guard against reserved slugs that go to authenticated pages
    switch c.Param("slug") {
    case "edit", "manage":
        c.Response().Header().Set("Cache-Control", "no-cache")
        return c.HTMLBlob(http.StatusOK, indexHTML)
    }
    // ... SSR logic
})

// Authenticated routes: NEVER SSR
e.GET("/dashboard/*", func(c echo.Context) error {
    c.Response().Header().Set("Cache-Control", "no-cache, no-store")
    return c.HTMLBlob(http.StatusOK, indexHTML)
})
```

See `docs/01-ssr-trigger-routes.md` for the full pattern.

### 3.2 The `initialData` contract

**Files:** `cmd/web/main.go` (Go struct) + `frontend/src/contexts/SSRContext.tsx` (TypeScript interface)
**What:** The data Go passes to React at render time

Define a Go struct for each page type that needs pre-populated data:

```go
// In cmd/web/main.go
type PostInitialData struct {
    ID       string `json:"id"`
    Title    string `json:"title"`
    Body     string `json:"body"`
    CoverURL string `json:"coverUrl"`
}
```

In React, declare the matching interface:

```typescript
// In frontend/src/contexts/SSRContext.tsx (or a separate types file)
interface PostInitialData {
  id: string
  title: string
  body: string
  coverUrl: string | null
}
```

Use it in a component:

```typescript
const { initialData } = useSSRContext()
const post = initialData as PostInitialData
// post.title is available immediately, before any API call
```

**Critical:** The JSON field names in the Go struct tags (`json:"coverUrl"`) must exactly match the TypeScript field names. There is no compile-time check. See `docs/02-initialdata-contract.md`.

### 3.3 Critical CSS blob

**File:** `cmd/web/main.go`, the `criticalCSS` constant (line ~66)
**What:** Above-fold CSS inlined in `<head>` to eliminate render-blocking stylesheet

Replace the placeholder CSS with your real above-fold CSS:

1. Run `npm run build` to generate the hashed CSS file
2. Deploy or preview your app
3. Open Chrome DevTools → More Tools → Coverage → record a page load
4. Filter for your CSS file, copy the "green" (used on first paint) selectors
5. Minify and paste as the `criticalCSS` constant

See `docs/03-critical-css.md` for full extraction instructions.

### 3.4 Cache invalidation wiring

**Files:** `cmd/api/main.go` (header setter) + `cmd/web/main.go` (proxy interceptor)
**What:** When content is updated via the API, HTML cache entries must be evicted

In your API write handlers:

```go
func handleUpdatePost(c echo.Context) error {
    id := c.Param("id")
    // ... update in DB ...
    c.Response().Header().Set("X-App-Invalidate", id)
    return c.JSON(http.StatusOK, updated)
}
```

The web server's proxy `ModifyResponse` hook (in `cmd/web/main.go`) already reads `X-App-Invalidate` and calls `invalidateContentHTML(id)`. You only need to set the header in your API handlers.

Also update `invalidateContentHTML()` in `main.go` if your URL structure differs from `/content/:id/`.

See `docs/04-cache-invalidation.md`.

### 3.5 Window global name

**Files:** `cmd/web/main.go` (`ssrBootScript()` function) + `frontend/src/main.tsx` + `frontend/src/contexts/SSRContext.tsx`
**What:** The JavaScript global that carries server-injected data to the client

Default: `__APP_DATA__`

This name appears in three places and must be identical in all three:

1. `cmd/web/main.go` → `ssrBootScript()`:
   ```go
   return `<script>window.__APP_DATA__=` + string(payloadJSON) + `</script>`
   ```

2. `frontend/src/main.tsx`:
   ```typescript
   (window as unknown as { __APP_DATA__?: {...} }).__APP_DATA__ ?? {}
   ```

3. `frontend/src/contexts/SSRContext.tsx` (type declaration comment/usage)

You don't need to change this unless you have a naming collision.

### 3.6 Router configuration

**File:** `frontend/src/router.ts`
**What:** Your actual application routes

Replace the placeholder routes with real ones. The route tree defined here is used:
- In `App.tsx` for the client-side singleton router
- In `entry-server.tsx` per-request (with memory history) for SSR

Both must use the same route tree, otherwise SSR will render a different component than the client expects.

If you prefer React Router v6/v7 over TanStack Router, the swap requires updating:
- `frontend/src/router.ts` (use `createBrowserRouter` / `createStaticRouter`)
- `frontend/src/entry-server.tsx` (use `createStaticHandler` + `renderToPipeableStream`)
- `frontend/src/App.tsx` (use `RouterProvider` from react-router-dom)

---

## 4. Common mistakes and how to detect them

### 4.1 Hydration mismatch (React error #418 / #422)

**Symptom:** Browser console shows "Warning: Text content did not match. Server: '...' Client: '...'" or "Warning: An error occurred during hydration."

**Cause options:**

A. **`initialData` shape mismatch** — Go serialized field `CoverURL` but React reads `coverUrl` (different case). The SSR render shows the data correctly (Node has the right value), but the client render uses `undefined` for that field → different output.

B. **SSR-ing an authenticated route** — The sidecar renders the page without a session (anonymous state). The client hydrates as a logged-in user. The UIs differ.

C. **Date/time formatting difference** — Go formats `time.Time` as RFC 3339 with timezone. If your React component formats the same date differently, the rendered strings don't match.

D. **Random/nonce values** — If any component renders a random value (UUID, nonce, Math.random()), it will differ between server and client.

**Fix:** Find the differing content in the error message. Trace it back to the component. Check whether it comes from `initialData` (field name mismatch) or from a non-deterministic render.

### 4.2 SSR trigger on authenticated routes

**Symptom:** Logged-in users see a flash of anonymous content, then the correct UI appears. OR: the HTML cache contains a page with the anonymous header/nav, served to all users.

**Cause:** A route handler is calling `callSSR()` for a URL path that goes to an auth-gated React page.

**Detection:** In browser DevTools, view the page source (Cmd+U). If you see the React component tree rendered in `#root` (not just `<div id="root"></div>`) for an authenticated page, SSR is enabled for that route.

**Fix:** In the route handler for that path, return `indexHTML` directly without calling `callSSR()`. Set `Cache-Control: no-cache, no-store`.

### 4.3 Missing cache invalidation on write handlers

**Symptom:** Users update content, reload the page, and see the old content. The updated content appears after 10-40 minutes (TTL expiry).

**Cause:** The API handler that writes the updated content doesn't set `X-App-Invalidate`. The HTML cache holds the stale HTML until TTL+SWR expires.

**Detection:** After making an update via the API, check what `Cache-Control` the HTML page returns, and whether the cache key for that page is being evicted. Add a debug log in `invalidateContentHTML()` temporarily.

**Fix:** Add `c.Response().Header().Set("X-App-Invalidate", contentID)` to every API handler that modifies content.

### 4.4 `initialData` shape mismatch (silent hydration drift)

**Symptom:** The page renders correctly visually (SSR HTML looks right), but data in React state is wrong. For example, a "published date" shows as undefined in the component even though the SSR HTML showed it correctly.

**Cause:** The SSR render used the data directly from Go. The client hydration used `window.__APP_DATA__` where a field was named differently. React adopted the SSR DOM (so the HTML is correct) but the component's internal state has the wrong value from `initialData`.

**Detection:** Open DevTools → Application → check `window.__APP_DATA__` in the console. Compare the field names against the TypeScript interface.

**Fix:** Update the Go struct `json:""` tags to match the TypeScript interface exactly.

### 4.5 Circuit breaker stays open permanently

**Symptom:** All pages serve bare `index.html` (CSR only), SSR never activates. Logs show "SSR circuit breaker open" on every request.

**Cause options:**

A. **`node` not in PATH** — The Go binary can't find Node.js. Check with `which node`.

B. **`dist-ssr/entry-server.js` not found** — The SSR bundle wasn't built or isn't in the expected location relative to the binary. Check the path in `startSSRServer()`.

C. **Node spawned in Dockerfile CMD** — If `Dockerfile.web` includes `node ssr-server.mjs` as a CMD or RUN command alongside the Go binary, Node will already hold port 3010. When Go spawns its own Node instance, it gets EADDRINUSE, which opens the circuit breaker.

**Fix:** Check logs for "SSR: node not in PATH" or "SSR: failed to start". Ensure `Dockerfile.web` only runs `./app-web` as CMD.

### 4.6 EADDRINUSE in Docker

**Symptom:** Works locally but SSR fails in Docker. Log: "Error: listen EADDRINUSE: address already in use :::3010".

**Cause:** The Dockerfile has two things starting Node: the Go binary (which calls `startSSRServer()`) AND a separate `CMD` or `ENTRYPOINT` starting Node.

**Fix:** The Dockerfile.web `CMD` must be only `["./app-web"]`. Go owns the Node lifecycle. Never start Node in the Dockerfile alongside the Go binary.

---

## 5. The ISR-like caching pattern

The HTML cache in `cmd/web/main.go` implements an ISR-like pattern using `sync.Map` and goroutines.

**Data structures:**
- `htmlCache sync.Map` — maps URL path → `htmlCacheEntry{html, expires, staleExpires}`
- `htmlCacheRefresh sync.Map` — prevents duplicate background refresh goroutines

**Entry states:**
- Fresh (now < expires): serve immediately
- Stale (expires < now < staleExpires): serve immediately + trigger one background refresh
- Expired (now > staleExpires): evict, treat as cache miss

**Functions:**
- `getCachedHTMLState(key)` → `(html, found, isStale)` — the main read
- `setCachedHTML(key, html)` — store with standard TTL/SWR
- `setStaticHTML(key, html)` — store with 24h TTL/SWR (for "rarely changes" content)
- `refreshCachedHTML(key, renderFn, ttl, swr)` — spawn one background goroutine
- `invalidateCacheKey(key)` — evict immediately

**Constants (customize in main.go):**
- `htmlCacheMax = 500` — max entries; new entries dropped if exceeded
- `htmlCacheTTL = 10 * time.Minute` — fresh window
- `htmlCacheSWR = 30 * time.Minute` — stale window (adds to TTL)
- `htmlStaticTTL = 24 * time.Hour` — for static content
- `htmlStaticSWR = 24 * time.Hour`
- `publicHTMLCacheCC = "public, max-age=60, stale-while-revalidate=300"` — browser/CDN header

**Startup pre-warm:** `prewarmHTMLCache()` runs in a goroutine at startup. It waits up to 20s for the SSR sidecar to be ready, then renders and caches important pages. This prevents the first real visitor from experiencing the full SSR render time. Customize this function with your own DB queries.

---

## 6. LCP optimization patterns

In priority order:

1. **SSR** (biggest impact): hero `<img>` is in the initial HTML, browser discovers it immediately
2. **`<link rel="preload" as="image" fetchpriority="high">`**: injected by `injectSSRContent()` — browser fetches cover image in parallel with JS
3. **Critical CSS** (100-500ms): inlined by `optimizeIndexHTML()` — no render-blocking stylesheet request
4. **Async stylesheet**: `onload="this.onload=null;this.rel='stylesheet'"` — main CSS doesn't block paint
5. **Cloudflare edge cache** (drops TTFB from 500ms to 50ms for repeat visitors)
6. **Preconnect hints**: in `index.html` for your image CDN
7. **Client-side WebP compression**: before upload, 1920px max, 82% quality — keeps images fast for all future visitors
8. **Defer analytics**: `window.addEventListener('load', ...)` — doesn't compete with critical resources
9. **`loading="lazy"`** on below-fold images — never becomes LCP candidate

---

## 7. The circuit breaker

The circuit breaker (`ssrAvailable int32`) prevents a slow or down Node sidecar from making every Go request wait 1500ms before timing out.

**States:**
- `1` (atomic): SSR is available — `callSSR()` attempts the request
- `0` (atomic): SSR is unavailable — `callSSR()` returns immediately with an error

**What opens it (sets to 0):**
- HTTP error from the sidecar (connection refused, etc.)
- Node process exits unexpectedly (monitored in `startSSRServer()` goroutine)

**What resets it (sets to 1):**
- Automatic after 30 seconds (`time.AfterFunc(30*time.Second, ...)`)
- Startup readiness poll success

**Startup behavior:** `startSSRServer()` sets `ssrAvailable = 0` immediately (so early requests during the ~2-3s Node startup use CSR), then a readiness poll goroutine pings the sidecar every 500ms until it responds. On success: `ssrAvailable = 1`. On 15s timeout: `ssrAvailable = 1` anyway (optimistic).

**Fallback behavior:** When `callSSR()` returns an error, route handlers call `injectCoverPreload(indexHTML, coverImage)` as a lighter optimization — the cover image preload still fires, improving LCP even in CSR mode.

---

## 8. Testing checklist

### Verify SSR is active

1. Start the server: `go run ./cmd/web`
2. Check logs: you should see "SSR: started (pid ...)" then "SSR: server ready"
3. `curl -s http://localhost:3000/content/abc/my-page | grep 'id="root"'`
   - SSR active: `<div id="root"><div ...>` (React tree inside root)
   - SSR inactive: `<div id="root"></div>` (empty)

### Verify hydration is working

1. Open http://localhost:3000/content/abc/my-page in Chrome
2. Open DevTools → Console
3. No "Warning: Text content did not match" messages = clean hydration
4. No React error #418/#422 messages

### Verify window.__APP_DATA__

```javascript
// In browser DevTools console:
window.__APP_DATA__
// Should be an object with { coverImage, initialData }
// Check that field names match your TypeScript interface
```

### Verify cache is working

```bash
# First request: cache miss (MISS in logs or measured latency)
time curl -s http://localhost:3000/content/abc/my-page > /dev/null

# Second request: cache hit (should be < 5ms)
time curl -s http://localhost:3000/content/abc/my-page > /dev/null
```

### Verify SSR fallback (circuit breaker)

1. Stop the Node sidecar manually: `kill $(lsof -t -i:3010)`
2. Request the page: `curl -s http://localhost:3000/content/abc/my-page | grep 'id="root"'`
3. Should see empty root: `<div id="root"></div>` (CSR fallback)
4. After 30s, SSR should resume

### Run Lighthouse locally

```bash
# Build first
cd frontend && npm run build && npm run build:ssr && cd ..
cp -r frontend/dist static/dist/
go build -o /tmp/app-web ./cmd/web
/tmp/app-web &
npx lighthouse http://localhost:3000 --only-categories=performance --view
```

Target: LCP < 2.5s, FCP < 1.5s on simulated mobile throttling.

### Check for leaked SOGS names

If you copied this from the SowOneGoodSeed codebase, verify no SOGS-specific references remain:

```bash
grep -ri "sogs\|sowgood\|sowonegoodseed\|__SOGS_HERO__\|sowonegoodseed/sogs" . \
  --exclude-dir=.git --exclude-dir=node_modules
```

All should return empty.
