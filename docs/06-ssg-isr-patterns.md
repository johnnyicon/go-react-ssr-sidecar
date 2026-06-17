# SSG / ISR Patterns

This template supports three rendering modes. Choosing the right one per route significantly affects performance and infrastructure cost.

## Mode 1: SSR (Server-Side Rendering)

**Render on every request via the Node sidecar.**

- Go receives a request
- Go calls `callSSR()` → Node renders React → Go injects HTML → response sent
- No caching in this path

**When to use:** Real-time content that changes on every request. Very few pages actually need this.

**Cost:** Node SSR process time (typically 50-200ms) + DB query on every request. At scale, you'll saturate the Node sidecar before the Go server.

## Mode 2: ISR-like (Incremental Static Regeneration)

**Render once, cache in Go's sync.Map, revalidate in background.**

This is the default for content detail pages in this template.

The flow:
1. First request: cache miss → DB query + SSR render → store in `sync.Map`
2. Requests during TTL (10min): cache hit → serve cached HTML instantly
3. Requests during SWR window (10-40min after first render): serve stale, trigger background goroutine to re-render
4. Background goroutine: DB query + SSR render → update cache
5. After TTL + SWR (40min total): evict, back to step 1

Cache invalidation via `X-App-Invalidate` can force step 1 at any time. See `docs/04-cache-invalidation.md`.

```go
// ISR-like pattern in a route handler:
cacheKey := c.Request().URL.RequestURI()

if cached, ok, stale := getCachedHTMLState(cacheKey); ok {
    c.Response().Header().Set("Cache-Control", publicHTMLCacheCC)
    if stale {
        // Serve stale immediately, refresh in background
        refreshCachedHTML(cacheKey, func() ([]byte, error) {
            item, err := db.GetItem(ctx, id)
            if err != nil { return nil, err }
            ssrHTML, err := callSSR(cacheKey, item.CoverURL, buildInitialData(item))
            if err != nil { return nil, err }
            return injectSSRContent(indexHTML, ssrHTML, item.CoverURL, buildInitialData(item)), nil
        }, htmlStaticTTL, htmlStaticSWR)
    }
    return c.HTMLBlob(http.StatusOK, cached)
}

// Cache miss: render synchronously
item, err := db.GetItem(c.Request().Context(), id)
// ...
out := injectSSRContent(indexHTML, ssrHTML, item.CoverURL, initialData)
setStaticHTML(cacheKey, out)
return c.HTMLBlob(http.StatusOK, out)
```

**When to use:** Content that updates occasionally (minutes to hours). Most blog posts, product pages, content detail pages.

**Cost:** One DB query + SSR render per cache miss. At steady state, nearly zero — all requests are cache hits.

## Mode 3: SSG (Static Site Generation)

**Pre-render to HTML files at build time, serve directly.**

For pages that never change (or change only on deploy), pre-render them and embed the HTML directly. No DB query, no Node at request time.

### Build-time generation

Add a `cmd/generate` binary that:
1. Queries all static content
2. Calls the Node SSR sidecar
3. Writes `.html` files to `static/dist/`

```go
// cmd/generate/main.go (not included in this template — add if needed)
// Run as: go run ./cmd/generate before go build ./cmd/web
items, _ := db.ListStaticItems(ctx)
for _, item := range items {
    ssrHTML, _ := callSSR("/items/"+item.ID, item.CoverURL, buildData(item))
    out := injectSSRContent(indexHTML, ssrHTML, item.CoverURL, buildData(item))
    os.WriteFile("static/dist/items/"+item.ID+"/index.html", out, 0644)
}
```

The `makeSPAHandler` already serves known files from the embedded FS before falling back to `index.html`. Pre-generated `.html` files are served directly, bypassing the SSR path entirely.

**When to use:** Legal pages, about pages, fixed reference content, objective/category lists that change only with code deploys.

**Cost:** Zero at runtime. Small build-time overhead.

## Choosing the right mode

| Content type | Changes how often | Right mode |
|---|---|---|
| Legal, about, FAQ | Never (only deploys) | SSG |
| Category/tag index pages | Rarely | SSG or ISR (long TTL) |
| Content detail pages | Minutes to hours | ISR |
| Home/featured content | Minutes to hours | ISR with explicit invalidation |
| Search results | Per request | SSR (or skip SSR entirely) |
| User dashboard | Per user | CSR only (never SSR) |
| Real-time data (prices, scores) | Seconds | CSR only |

## Pre-warming the ISR cache

At startup, `prewarmHTMLCache()` renders and caches important pages before the first real visitor arrives. This prevents the first visitor from experiencing the SSR render time. See the `prewarmHTMLCache()` function in `cmd/web/main.go`.

The pre-warm waits for the SSR sidecar to pass its readiness poll (up to 20s) before rendering. This prevents a flood of failed callSSR() calls that would open the circuit breaker.
