# Cloudflare Cache Rules

## Why Cloudflare matters for SSR performance

Without a CDN, every request hits your Railway origin: Go → Node SSR → DB query. On Railway, this is ~200-500ms per request from typical user locations.

With Cloudflare:
- First request: hits origin (500ms)
- Subsequent requests: served from Cloudflare edge (<50ms, often 10-20ms)

The Go HTML cache (in-memory, per-process) is a second layer: it eliminates DB + SSR overhead for the origin. Cloudflare is the first layer: it eliminates the origin entirely for cached content.

## Setting up Cloudflare cache rules

Go to Cloudflare Dashboard → your domain → Rules → Cache Rules.

### Rule 1: Cache SSR HTML pages

```
If: hostname matches "your-app.com"
  AND URL path matches regex:
    ^/(content/[^/]+/[^/]+|blog|$)
  AND NOT (cookie name "session" exists)
Then:
  Edge TTL: 60 seconds
  Browser TTL: 60 seconds
  Stale-while-revalidate: 300 seconds
  Cache status: Eligible for cache
```

The critical clause: `NOT (cookie name "session" exists)`.

Logged-in users have a session cookie. If you cache their request, the next anonymous visitor gets their data. This rule bypasses Cloudflare cache for authenticated users and lets their request fall through to origin (which serves their personalized content from the SPA).

### Rule 2: Never cache API routes

```
If: URL path starts with "/api/"
Then:
  Cache status: Bypass
```

API responses contain user-specific data and must never be cached by Cloudflare.

### Rule 3: Long cache for static assets

Vite adds content hashes to all filenames in `/assets/`. These are safe to cache forever.

```
If: URL path starts with "/assets/"
Then:
  Edge TTL: 1 year
  Browser TTL: 1 year
  Cache status: Eligible for cache
```

The Go handler already sets `Cache-Control: public, max-age=31536000, immutable` for these. Cloudflare respects this but you can also set it explicitly.

## Cache-Control header alignment

The Go web server sets `Cache-Control: public, max-age=60, stale-while-revalidate=300` for SSR HTML pages (the `publicHTMLCacheCC` constant in `main.go`). Cloudflare respects this:

- 60s: serve from Cloudflare cache without revalidating
- After 60s: serve stale, trigger background revalidation
- After 360s (60s + 300s SWR): evict from edge cache, fetch fresh from origin

You can tighten or loosen these values:
- More aggressive caching: increase `max-age` and `stale-while-revalidate`
- More real-time: set `no-cache` (Cloudflare revalidates every request)

## The authenticated user problem in detail

The session cookie bypass is non-negotiable. Here's why:

1. User A (anonymous) visits `/content/abc`. Cloudflare caches the anonymous HTML.
2. User B (logged in, has "Add to Favorites" button in header) visits `/content/abc`.
3. Without the bypass: User B gets the anonymous HTML from Cloudflare cache. Their "Add to Favorites" button never appears.
4. With the bypass: User B's request goes to origin → SSR renders with no session data → React hydrates → user-specific UI appears on the client.

Wait — step 4 still doesn't show user-specific UI at SSR time. That's correct and intentional. SSR only handles public content (title, description, cover image, body text). User-specific UI (favorites, follow buttons, ownership actions) is rendered client-side by React after hydration.

## Cloudflare Tiered Cache

Enable Cloudflare's "Tiered Cache" (also called "Argo Smart Routing" in older docs) to cache at Cloudflare's regional data centers rather than only at edge nodes. This significantly improves cache hit rates for globally distributed traffic.

Enable it at: Cloudflare Dashboard → Caching → Tiered Cache → Smart Tiered Cache Topology.

## Testing your Cloudflare setup

Check the `CF-Cache-Status` response header:
- `HIT` — served from Cloudflare cache (fast path)
- `MISS` — origin was hit, response is now cached
- `BYPASS` — caching was bypassed (e.g. authenticated request)
- `EXPIRED` — cached entry expired, origin was hit for fresh content
- `REVALIDATED` — served stale while revalidating in background

```bash
curl -I https://your-app.com/content/abc123/my-post | grep -i cf-cache
# CF-Cache-Status: HIT
```
