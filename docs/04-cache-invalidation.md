# Cache Invalidation

## The architecture constraint

The web server (`cmd/web`) and API server (`cmd/api`) are separate processes (separate Railway services in production). The HTML cache lives in-memory in the web server process. The API server has no direct access to it.

Invalidation happens through the proxy: when the web server proxies `/api/*` to the API server, the `apiProxy.ModifyResponse` hook inspects response headers. If the API returns `X-App-Invalidate: <id>`, the web server evicts the matching cache entries.

## How it works

### API side: signal invalidation

In your API write handlers, after modifying content, set the invalidation header:

```go
// cmd/api/main.go — example update handler
func handleUpdatePost(c echo.Context) error {
    id := c.Param("id")
    // ... update the post in DB ...

    // Signal the web server to evict HTML cache for this post
    c.Response().Header().Set("X-App-Invalidate", id)
    return c.JSON(http.StatusOK, updatedPost)
}
```

You can invalidate multiple IDs at once (comma-separated):

```go
c.Response().Header().Set("X-App-Invalidate", postID+","+authorID)
```

### Web server side: receive and evict

In `cmd/web/main.go`, the proxy's `ModifyResponse` intercepts the header:

```go
apiProxy.ModifyResponse = func(resp *http.Response) error {
    if idHeader := resp.Header.Get("X-App-Invalidate"); idHeader != "" {
        for _, id := range strings.Split(idHeader, ",") {
            invalidateContentHTML(strings.TrimSpace(id))
        }
        resp.Header.Del("X-App-Invalidate") // don't forward to the browser
    }
    return nil
}
```

`invalidateContentHTML(id)` scans the `sync.Map` for cache keys containing `/id/` or `/id` and evicts them, plus the home page (which often shows recently-updated content).

## What gets invalidated

The `invalidateContentHTML` function is path-pattern-based: it evicts any cache key that `strings.Contains(key, "/"+id)`. This means:

- `/content/abc123/my-title` — evicted if id is "abc123"
- `/content/abc123/my-old-title` — also evicted (slug change)
- `home/ssr/` — always evicted (home page shows recent content)
- `home/` — always evicted

CUSTOMIZE: If your URL structure is different, update `invalidateContentHTML()` in `main.go`.

## Direct cache operations (no proxy)

If your use case doesn't go through the proxy (e.g. a background job updates content), you need a different mechanism. Options:

### Option 1: HTTP endpoint (simple)

Add a protected invalidation endpoint to the web server:

```go
e.POST("/internal/invalidate", func(c echo.Context) error {
    // Gate with a shared secret
    if c.Request().Header.Get("X-Internal-Token") != os.Getenv("INTERNAL_TOKEN") {
        return c.NoContent(http.StatusUnauthorized)
    }
    var body struct { ID string `json:"id"` }
    if err := c.Bind(&body); err != nil {
        return c.NoContent(http.StatusBadRequest)
    }
    invalidateContentHTML(body.ID)
    return c.NoContent(http.StatusOK)
}, middleware.OnlyInternalNetwork)
```

### Option 2: Full cache flush

For bulk content updates (e.g. after a migration), flush everything:

```go
func flushHTMLCache() {
    htmlCache.Range(func(key, _ any) bool {
        htmlCache.Delete(key)
        return true
    })
    htmlCacheRefresh.Range(func(key, _ any) bool {
        htmlCacheRefresh.Delete(key)
        return true
    })
    log.Println("HTML cache flushed")
}
```

## The TTL safety net

Even without explicit invalidation, stale entries expire:
- `htmlCacheTTL` (10min): fresh window — always served
- `htmlCacheSWR` (30min): stale window — served but rerendered in background
- After TTL + SWR (40min total): entry is evicted and will be re-rendered on next request

This means even if you miss an invalidation event, the maximum staleness is 40 minutes. For most content apps, this is acceptable.

For content that must be real-time accurate, don't cache it: set `Cache-Control: no-cache` and skip `getCachedHTMLState()` and `setStaticHTML()`.
