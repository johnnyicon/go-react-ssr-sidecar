# SSR Trigger Routes

## The decision: which routes get server-rendered?

SSR is not free. Each SSR response requires a synchronous call to the Node sidecar and (in most implementations) a DB query. The right call: SSR only routes where the benefit (LCP improvement, SEO, social share previews) outweighs the cost.

## Rules for SSR-eligible routes

**SSR these:**
- Public content detail pages (e.g. `/posts/:id/:slug`, `/products/:id`)
- Home page / landing page
- Public list pages (e.g. `/blog`, `/categories/:slug`)
- Any page where the URL is predictable and the content is fetched without authentication

**Do NOT SSR these:**
- Authenticated pages (dashboard, settings, account pages)
- Pages with user-specific data
- Form pages (create, edit, submit)
- Admin interfaces

## The critical mistake: SSR-ing authenticated routes

If you SSR a page that shows per-user data and cache the result, every visitor — including logged-in users — will see the same HTML. The CDN serves the first visitor's data to everyone.

Even without CDN caching, the circuit breaker doesn't protect you here. The sidecar renders without session cookies, so it renders the anonymous state. React hydrates against a user-authenticated React tree. The HTML doesn't match → React error #422 (text content mismatch) or #418 (server/client HTML mismatch). The page still works after hydration, but the flash of anonymous content is bad UX.

**The fix:** In `cmd/web/main.go`, the route handler must check whether the route is an authenticated route before calling `callSSR()`. Pattern:

```go
e.GET("/dashboard/*", func(c echo.Context) error {
    // NEVER call callSSR() here — always serve bare indexHTML
    c.Response().Header().Set("Cache-Control", "no-cache, no-store")
    return c.HTMLBlob(http.StatusOK, indexHTML)
})
```

## Reserved slug protection

Some URL patterns like `/content/:id/:slug` have reserved slugs that route to different pages than the content detail view:

```go
e.GET("/content/:id/:slug", func(c echo.Context) error {
    switch c.Param("slug") {
    case "edit", "manage", "settings":
        // These are auth-gated edit pages — never SSR
        c.Response().Header().Set("Cache-Control", "no-cache")
        return c.HTMLBlob(http.StatusOK, indexHTML)
    }
    // ... proceed with SSR for the detail page
})
```

Without this guard, a user visiting `/content/abc/edit` would get an SSR-rendered "content detail" page that hydrates against the edit page's React tree → hydration mismatch.

## Crawler handling

Social media crawlers (Facebook, Twitter, LinkedIn, WhatsApp, Slack, Discord) don't execute JavaScript. The `isCrawler()` function in `cmd/web/main.go` detects them and returns a minimal OG-tag HTML stub instead of the full SSR page.

Google and Bing are intentionally excluded from `crawlerAgents` — they render JavaScript and should see the real React SPA so they can index the full content.

Cache behavior for crawlers: always `Cache-Control: no-store`. Crawler responses contain dynamic OG data and should never be cached.

## Implementing a new SSR route

```go
e.GET("/my-content/:id/:slug", func(c echo.Context) error {
    id := c.Param("id")
    cacheKey := c.Request().URL.RequestURI()

    // 1. Crawler gets OG stub
    if isCrawler(c.Request().Header.Get("User-Agent")) {
        item, err := db.GetItem(ctx, id)
        if err != nil {
            return c.HTMLBlob(http.StatusOK, indexHTML)
        }
        c.Response().Header().Set("Cache-Control", "no-store")
        return serveOG(c, ogData{
            Title:       item.Title,
            Description: item.Description,
            Image:       item.CoverURL,
            URL:         siteBase + c.Request().URL.RequestURI(),
        })
    }

    // 2. Cache hit
    if cached, ok, stale := getCachedHTMLState(cacheKey); ok {
        c.Response().Header().Set("Cache-Control", publicHTMLCacheCC)
        if stale {
            refreshCachedHTML(cacheKey, func() ([]byte, error) {
                return renderItemHTML(cacheKey)
            }, htmlStaticTTL, htmlStaticSWR)
        }
        return c.HTMLBlob(http.StatusOK, cached)
    }

    // 3. Fetch data from DB
    item, err := db.GetItem(c.Request().Context(), id)
    if err != nil {
        return c.HTMLBlob(http.StatusOK, indexHTML)
    }

    // 4. SSR attempt
    c.Response().Header().Set("Cache-Control", publicHTMLCacheCC)
    initialData := buildInitialData(item)
    if ssrHTML, err := callSSR(cacheKey, item.CoverURL, initialData); err == nil {
        out := injectSSRContent(indexHTML, ssrHTML, item.CoverURL, initialData)
        setStaticHTML(cacheKey, out)
        return c.HTMLBlob(http.StatusOK, out)
    }

    // 5. SSR fallback — still inject cover preload for LCP
    out := injectCoverPreload(indexHTML, item.CoverURL)
    setCachedHTML(cacheKey, out)
    return c.HTMLBlob(http.StatusOK, out)
})
```
