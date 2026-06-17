// cmd/web — Go web server with embedded React SPA + Node SSR sidecar.
//
// This is the heart of the go-react-ssr-sidecar pattern. It:
//   - Embeds the compiled React frontend (static/dist/) via go:embed
//   - Spawns a Node.js SSR sidecar (ssr-server.mjs) as a child process
//   - For SSR-eligible routes: calls the sidecar, injects rendered HTML + boot data
//   - Maintains an in-memory HTML cache with TTL + stale-while-revalidate
//   - Falls back to bare index.html (CSR) if the sidecar is unavailable
//   - Proxies /api/* to a separate API service (optional — remove if monolith)
//
// Customize for your project:
//   1. SSR trigger routes — which paths call callSSR() vs. serve plain indexHTML
//   2. The initialData contract — your Go struct ↔ React interface (must match)
//   3. criticalCSS — replace with your own above-fold CSS blob
//   4. window.__APP_DATA__ — the global name injected by ssrBootScript()
//   5. Cache invalidation — wire X-App-Invalidate response header from API handlers
//   6. SITE_BASE / APP_NAME env vars — set for your domain
//
// See AGENT-GUIDE.md for a complete explanation of every customization point.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/joho/godotenv/autoload"
	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"
	appStatic "github.com/johnnnyicon/go-react-ssr-sidecar/static"
)

// ── Site constants ─────────────────────────────────────────────────────────
//
// CUSTOMIZE: Set SITE_BASE and APP_NAME environment variables in production.
// These values are used for OG tag generation and canonical link hrefs.

var (
	siteBase = getEnvOrDefault("SITE_BASE", "http://localhost:3000")
	appName  = getEnvOrDefault("APP_NAME", "My App")
)

func getEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ── Critical CSS ───────────────────────────────────────────────────────────
//
// CUSTOMIZE: Replace this blob with your own above-fold CSS. The strategy:
//   1. Run `npm run build` and `lighthouse --view` on your homepage
//   2. Open Coverage tab in Chrome DevTools, reload, filter "unused on first load"
//   3. The used CSS on first paint = your critical CSS
//   4. Minify it and paste it here
//
// Purpose: inlining critical CSS eliminates one render-blocking stylesheet
// request, improving FCP by 200-500ms on slow connections.
// See docs/03-critical-css.md for the full pattern.
//
// The stylesheetLinkRE below converts the main CSS <link> to async-load
// so it no longer blocks rendering, with a <noscript> fallback.
const criticalCSS = `/* REPLACE with your above-fold critical CSS */
*,:before,:after{box-sizing:border-box}
html{-webkit-text-size-adjust:100%}
body{margin:0;font-family:system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#fff;color:#0f172a}
a{color:inherit;text-decoration:inherit}
button{font:inherit}
img{display:block;max-width:100%;height:auto}
#root{min-height:100vh}`

// stylesheetLinkRE matches the Vite-generated hashed CSS asset link tag.
// The onload trick defers the full stylesheet load until after paint.
var stylesheetLinkRE = regexp.MustCompile(`<link rel="stylesheet"[^>]*href="/assets/index-[^"]+\.css"[^>]*>`)

// ── In-memory HTML cache ───────────────────────────────────────────────────
//
// Anonymous public pages (content detail pages, home page) are identical for
// every visitor. Without a cache, each request would do a DB query + Node SSR
// render synchronously. This cache stores the final HTML in memory with:
//   - TTL (10min): serve cached HTML for this duration
//   - SWR (30min after TTL = 40min total): serve stale HTML while refreshing in background
//   - Max entries (500): protect against unbounded memory growth
//
// Static content (home, detail pages) uses longer TTLs. Cache is invalidated
// when the API returns X-App-Invalidate with the content ID — see the
// apiProxy.ModifyResponse handler in main().
//
// See docs/04-cache-invalidation.md and docs/06-ssg-isr-patterns.md.

type htmlCacheEntry struct {
	html         []byte
	expires      time.Time
	staleExpires time.Time
}

var (
	htmlCache        sync.Map
	htmlCacheRefresh sync.Map

	// htmlCacheMax prevents unbounded memory growth. When the map reaches this
	// size, new entries are dropped (existing entries are still served).
	htmlCacheMax = 500

	// htmlCacheTTL is the freshness window for dynamic content pages.
	htmlCacheTTL = 10 * time.Minute

	// htmlCacheSWR is the stale-while-revalidate window added on top of TTL.
	// Total time a stale entry can be served: TTL + SWR = 40 minutes.
	htmlCacheSWR = 30 * time.Minute

	// htmlStaticTTL / htmlStaticSWR are used for pages whose content changes
	// rarely (home page, content detail pages). They are still invalidated
	// immediately via X-App-Invalidate when content is updated.
	htmlStaticTTL = 24 * time.Hour
	htmlStaticSWR = 24 * time.Hour

	// Cache-Control header sent to browsers and CDN for public HTML pages.
	// 60s TTL + 300s SWR lets Cloudflare serve stale while revalidating.
	publicHTMLCacheCC = "public, max-age=60, stale-while-revalidate=300"
)

// getCachedHTML returns cached HTML if present and not stale.
func getCachedHTML(key string) ([]byte, bool) {
	html, ok, stale := getCachedHTMLState(key)
	if !ok || stale {
		return nil, false
	}
	return html, true
}

// getCachedHTMLState returns (html, found, isStale).
// isStale=true means the entry is within the SWR window — serve it but refresh.
func getCachedHTMLState(key string) ([]byte, bool, bool) {
	v, ok := htmlCache.Load(key)
	if !ok {
		return nil, false, false
	}
	e := v.(htmlCacheEntry)
	now := time.Now()
	if now.After(e.staleExpires) {
		htmlCache.Delete(key)
		return nil, false, false
	}
	return e.html, true, now.After(e.expires)
}

func setCachedHTML(key string, html []byte) {
	setCachedHTMLWithTTL(key, html, htmlCacheTTL, htmlCacheSWR)
}

func setStaticHTML(key string, html []byte) {
	setCachedHTMLWithTTL(key, html, htmlStaticTTL, htmlStaticSWR)
}

func setCachedHTMLWithTTL(key string, html []byte, ttl, swr time.Duration) {
	// Don't grow the cache beyond htmlCacheMax entries.
	count := 0
	htmlCache.Range(func(_, _ any) bool { count++; return count < htmlCacheMax })
	if count >= htmlCacheMax {
		return
	}
	now := time.Now()
	htmlCache.Store(key, htmlCacheEntry{
		html:         html,
		expires:      now.Add(ttl),
		staleExpires: now.Add(ttl + swr),
	})
}

// refreshCachedHTML spawns a background goroutine to rerender a stale entry.
// LoadOrStore ensures only one goroutine renders at a time per key.
func refreshCachedHTML(key string, render func() ([]byte, error), ttl, swr time.Duration) {
	if _, loaded := htmlCacheRefresh.LoadOrStore(key, true); loaded {
		return // another goroutine is already refreshing this key
	}
	go func() {
		defer htmlCacheRefresh.Delete(key)
		html, err := render()
		if err != nil {
			log.Printf("html cache refresh failed for %s: %v", key, err)
			return
		}
		setCachedHTMLWithTTL(key, html, ttl, swr)
	}()
}

// invalidateCacheKey evicts a single key from both the content and refresh maps.
func invalidateCacheKey(key string) {
	htmlCache.Delete(key)
	htmlCacheRefresh.Delete(key)
}

// invalidateContentHTML evicts all cached pages that contain the given ID.
// CUSTOMIZE: adapt the key patterns to match your URL structure.
//
// Called when the API proxy sees an X-App-Invalidate response header.
// Example: your API handler for PUT /api/posts/:id sets
//   w.Header().Set("X-App-Invalidate", id)
// and the proxy middleware below calls invalidateContentHTML(id).
func invalidateContentHTML(id string) {
	if id == "" {
		return
	}
	// Evict any page whose cache key contains this ID.
	htmlCache.Range(func(key, _ any) bool {
		k, ok := key.(string)
		if !ok {
			return true
		}
		if strings.Contains(k, "/"+id+"/") || strings.Contains(k, "/"+id) {
			invalidateCacheKey(k)
		}
		return true
	})
	// Also evict the home page since it often shows recently-updated content.
	invalidateCacheKey("home/ssr/")
	invalidateCacheKey("home/")
}

// ── OG / crawler support ───────────────────────────────────────────────────
//
// Social media crawlers (Facebook, Twitter, LinkedIn, WhatsApp, Slack, Discord)
// don't execute JavaScript, so they'd see an empty <div id="root"></div>.
// For these crawlers we return a minimal HTML stub with OG meta tags.
//
// Google and Bing are intentionally excluded — they do execute JS and should
// see the real React SPA so they can index the fully-rendered content.

var crawlerAgents = []string{
	"facebookexternalhit", "facebot",
	"twitterbot",
	"linkedinbot",
	"whatsapp",
	"slackbot",
	"discordbot",
	"telegrambot",
	"pinterest",
	"embedly",
	"iframely",
}

func isCrawler(ua string) bool {
	ua = strings.ToLower(ua)
	for _, bot := range crawlerAgents {
		if strings.Contains(ua, bot) {
			return true
		}
	}
	return false
}

type ogData struct {
	Title       string
	Description string
	Image       string
	URL         string
	SiteName    string
}

// CUSTOMIZE: Update og:site_name and the footer link text.
var ogTmpl = template.Must(template.New("og").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <title>{{.Title}} — {{.SiteName}}</title>
  <meta name="description" content="{{.Description}}" />
  <meta property="og:type" content="website" />
  <meta property="og:site_name" content="{{.SiteName}}" />
  <meta property="og:url" content="{{.URL}}" />
  <meta property="og:title" content="{{.Title}} — {{.SiteName}}" />
  <meta property="og:description" content="{{.Description}}" />
  <meta property="og:image" content="{{.Image}}" />
  <meta property="og:image:width" content="1200" />
  <meta property="og:image:height" content="630" />
  <meta name="twitter:card" content="summary_large_image" />
  <meta name="twitter:title" content="{{.Title}} — {{.SiteName}}" />
  <meta name="twitter:description" content="{{.Description}}" />
  <meta name="twitter:image" content="{{.Image}}" />
  <link rel="canonical" href="{{.URL}}" />
</head>
<body>
  <h1>{{.Title}}</h1>
  <p>{{.Description}}</p>
  <a href="{{.URL}}">View on {{.SiteName}}</a>
</body>
</html>`))

func serveOG(c echo.Context, data ogData) error {
	data.SiteName = appName
	c.Response().Header().Set("Content-Type", "text/html; charset=utf-8")
	return ogTmpl.Execute(c.Response().Writer, data)
}

// ── HTML optimization ──────────────────────────────────────────────────────

// optimizeIndexHTML inlines critical CSS and converts the main stylesheet to
// async-load. Called once at startup on the embedded index.html.
//
// The stylesheet trick:
//   <link rel="preload" as="style" onload="this.onload=null;this.rel='stylesheet'" ...>
//   <noscript><link rel="stylesheet" ...></noscript>
//
// This eliminates the render-blocking stylesheet while still loading it ASAP.
// See docs/03-critical-css.md for details.
func optimizeIndexHTML(indexHTML []byte) []byte {
	// 1. Inline critical CSS just before </head>
	out := bytes.Replace(indexHTML,
		[]byte("</head>"),
		[]byte("<style>"+criticalCSS+"</style>\n</head>"),
		1,
	)
	// 2. Convert the hashed CSS link to async-load with a noscript fallback
	out = stylesheetLinkRE.ReplaceAllFunc(out, func(link []byte) []byte {
		preload := bytes.Replace(link,
			[]byte(`rel="stylesheet"`),
			[]byte(`rel="preload" as="style" onload="this.onload=null;this.rel='stylesheet'"`),
			1,
		)
		return []byte(string(preload) + "\n<noscript>" + string(link) + "</noscript>")
	})
	return out
}

// ── SSR sidecar ───────────────────────────────────────────────────────────
//
// The Node.js SSR sidecar (ssr-server.mjs) runs as a child process of the Go
// binary. Go sends render requests over HTTP; the sidecar returns the React
// component tree as an HTML string. Go then injects that string into #root.
//
// Circuit breaker: ssrAvailable (int32) is 0 when the sidecar is unhealthy.
// When the breaker is open, callSSR() immediately returns an error and the
// caller falls back to serving plain index.html (CSR mode). The breaker
// resets automatically after 30 seconds.
//
// See docs/07-lcp-optimization.md for why SSR dramatically improves LCP.

// ssrAvailable: 1 = healthy, 0 = circuit breaker open (sidecar unavailable).
var ssrAvailable int32 = 1

// ssrHTTPClient has a tight timeout. SSR render should be fast; if it's not,
// falling back to CSR is better than blocking the response for 1.5s.
var ssrHTTPClient = &http.Client{Timeout: 1500 * time.Millisecond}

// callSSR sends a render request to the Node SSR sidecar and returns the
// inner HTML for the React component tree (not a full document — just the
// content that goes inside <div id="root">).
//
// Parameters:
//   - requestPath: the URL path the browser requested (e.g. "/posts/123/my-title")
//   - coverImage: hero/cover image URL or "" if none; injected as a preload hint
//   - initialData: any Go struct that matches the React initialData interface.
//     MUST be JSON-serializable and MUST exactly match the TypeScript interface
//     in SSRContext.tsx. See docs/02-initialdata-contract.md.
//
// Returns ("", err) when the circuit breaker is open, on timeout, or HTTP error.
// Callers should fall back to serving indexHTML directly.
func callSSR(requestPath, coverImage string, initialData any) (string, error) {
	if atomic.LoadInt32(&ssrAvailable) == 0 {
		return "", fmt.Errorf("SSR circuit breaker open")
	}

	payload, _ := json.Marshal(map[string]any{
		"url":         requestPath,
		"coverImage":  coverImage,
		"initialData": initialData,
	})

	ssrURL := os.Getenv("SSR_SERVER_URL")
	if ssrURL == "" {
		ssrURL = "http://localhost:3010/render"
	}

	resp, err := ssrHTTPClient.Post(ssrURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		// HTTP error → open the circuit breaker for 30s so subsequent requests
		// don't pile up waiting for a dead sidecar.
		atomic.StoreInt32(&ssrAvailable, 0)
		go func() {
			time.Sleep(30 * time.Second)
			atomic.StoreInt32(&ssrAvailable, 1)
			log.Println("SSR: circuit breaker reset after 30s")
		}()
		return "", fmt.Errorf("SSR unavailable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("SSR error: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("SSR read: %w", err)
	}
	return string(body), nil
}

// ssrBootScript builds the <script> tag that seeds window.__APP_DATA__ with
// server-injected data. React reads this in main.tsx to initialize SSRContext
// before hydration. coverImage and initialData must match exactly what was
// passed to callSSR() so the hydration snapshot is identical.
//
// CUSTOMIZE: The global name "__APP_DATA__" must match in:
//   - This function (Go injection)
//   - frontend/src/main.tsx (window.__APP_DATA__ read)
//   - frontend/src/contexts/SSRContext.tsx (type declaration)
func ssrBootScript(coverImage string, initialData any) string {
	payload := map[string]any{"coverImage": coverImage}
	if initialData != nil {
		payload["initialData"] = initialData
	}
	payloadJSON, _ := json.Marshal(payload)
	return `<script>window.__APP_DATA__=` + string(payloadJSON) + `</script>`
}

// injectSSRContent replaces <div id="root"></div> with server-rendered HTML
// and injects cover image preload + boot script into <head>.
//
// This is the core of the SSR injection. After this call, the HTML document:
//   1. Has the React component tree pre-rendered in #root
//   2. Has <link rel="preload" as="image"> for the cover image (LCP optimization)
//   3. Has window.__APP_DATA__ so React hydration matches the server render
//
// extraHeadTags are injected before the boot script (e.g. API fetch preloads).
func injectSSRContent(indexHTML []byte, ssrHTML, coverImage string, initialData any, extraHeadTags ...string) []byte {
	headTags := []string{}
	if coverImage != "" {
		// Inject responsive preload hints so the browser fetches the cover image
		// in parallel with JS bundles. fetchpriority=high makes it the top fetch.
		mobile := `<link rel="preload" as="image" href="` + html.EscapeString(coverImage) + `" media="(max-width: 768px)" fetchpriority="high">`
		desktop := `<link rel="preload" as="image" href="` + html.EscapeString(coverImage) + `" media="(min-width: 769px)" fetchpriority="high">`
		headTags = append(headTags, mobile, desktop)
	}
	headTags = append(headTags, extraHeadTags...)
	headTags = append(headTags, ssrBootScript(coverImage, initialData))

	result := bytes.Replace(indexHTML,
		[]byte("</head>"),
		[]byte(strings.Join(headTags, "\n")+"\n</head>"),
		1,
	)
	// Inject SSR HTML into #root so React's hydrateRoot() adopts the existing DOM
	// rather than doing a full re-render. If the SSR HTML matches the CSR output
	// exactly, hydration is silent. If they differ, React logs hydration warnings.
	result = bytes.Replace(result,
		[]byte(`<div id="root"></div>`),
		[]byte(`<div id="root">`+ssrHTML+`</div>`),
		1,
	)
	return result
}

// injectCoverPreload injects a cover image preload hint into index.html without
// SSR. Used for pages where SSR is unavailable but we still know the cover URL
// (e.g. CSR fallback for content detail pages). Improves LCP even in CSR mode.
func injectCoverPreload(indexHTML []byte, coverImage string) []byte {
	if coverImage == "" {
		return indexHTML
	}
	preload := `<link rel="preload" as="image" href="` + html.EscapeString(coverImage) + `" fetchpriority="high">`
	return bytes.Replace(indexHTML, []byte("</head>"), []byte(preload+"\n</head>"), 1)
}

// ── SSR sidecar lifecycle ──────────────────────────────────────────────────

// startSSRServer spawns the Node SSR sidecar as a child process.
// Called once at startup. If Node is not in PATH or the script is missing,
// SSR is disabled — Go falls back to CSR for all routes.
//
// The circuit breaker (ssrAvailable) starts at 0 during startup and is set
// to 1 once the readiness poll succeeds. This prevents early requests from
// hitting connection-refused (which would latch the breaker open for 30s).
//
// IMPORTANT: The Dockerfile runs only the Go binary (not `node ssr-server.mjs`
// as a separate CMD). Go owns the Node process lifecycle. Starting Node in
// Dockerfile CMD as well causes EADDRINUSE — the Go-spawned Node instance
// finds the port occupied and fails, permanently opening the circuit breaker.
func startSSRServer() {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		log.Println("SSR: node not in PATH — SSR disabled, falling back to SPA")
		atomic.StoreInt32(&ssrAvailable, 0)
		return
	}

	script := os.Getenv("SSR_SCRIPT")
	if script == "" {
		// Default: same directory as the binary. In Docker, Go binary and
		// ssr-server.mjs are both in /app.
		exe, err := os.Executable()
		if err != nil || exe == "" {
			exe = "."
		}
		script = filepath.Join(filepath.Dir(exe), "ssr-server.mjs")
	}

	cmd := exec.Command(nodePath, script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		log.Printf("SSR: failed to start %s: %v — falling back to SPA", script, err)
		atomic.StoreInt32(&ssrAvailable, 0)
		return
	}
	log.Printf("SSR: started (pid %d, script %s)", cmd.Process.Pid, script)

	// Monitor the child process. If it exits unexpectedly, open the circuit
	// breaker for 30s (same policy as HTTP error path).
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("SSR: process exited: %v — circuit breaker open, resetting in 30s", err)
			atomic.StoreInt32(&ssrAvailable, 0)
			time.AfterFunc(30*time.Second, func() {
				atomic.StoreInt32(&ssrAvailable, 1)
				log.Println("SSR: circuit breaker reset after process exit")
			})
		}
	}()

	// Readiness poll: Node takes a few seconds to parse and execute the SSR bundle.
	// Hold ssrAvailable=0 until the sidecar accepts a request. If polling times
	// out after 15s, enable SSR optimistically (live circuit breaker handles errors).
	atomic.StoreInt32(&ssrAvailable, 0)
	go func() {
		ssrURL := os.Getenv("SSR_SERVER_URL")
		if ssrURL == "" {
			ssrURL = "http://localhost:3010/render"
		}
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := ssrHTTPClient.Post(ssrURL, "application/json",
				strings.NewReader(`{"url":"/","coverImage":null,"initialData":null}`))
			if err == nil {
				resp.Body.Close()
				atomic.StoreInt32(&ssrAvailable, 1)
				log.Println("SSR: server ready")
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		atomic.StoreInt32(&ssrAvailable, 1)
		log.Println("SSR: readiness poll timed out after 15s — enabling anyway")
	}()
}

// waitForSSRReady blocks until ssrAvailable=1 or the timeout elapses.
// Used by the pre-warm goroutine to avoid rendering before the sidecar is ready.
func waitForSSRReady(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&ssrAvailable) == 1 {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return atomic.LoadInt32(&ssrAvailable) == 1
}

// ── Pre-warm cache ─────────────────────────────────────────────────────────
//
// At startup, after the SSR sidecar is ready, render and cache the most
// important pages (home, top content) so the first real visitor never waits.
//
// CUSTOMIZE: Replace the example pre-warm logic with your own content queries.
// The pattern: query your DB for published content → renderPageSSRHTML() → setStaticHTML().
func prewarmHTMLCache(ctx context.Context, indexHTML []byte) {
	go func() {
		if !waitForSSRReady(20 * time.Second) {
			log.Println("html prewarm: SSR sidecar not ready; skipping")
			return
		}
		bgCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		_ = bgCtx // replace with your own DB queries + render calls

		// CUSTOMIZE: Add your pre-warm logic here. Example:
		//   items, err := db.ListPublished(bgCtx)
		//   for _, item := range items {
		//       path := "/content/" + item.ID + "/" + item.Slug
		//       html, err := renderItemSSRHTML(bgCtx, indexHTML, item, path)
		//       if err == nil {
		//           setStaticHTML(path, html)
		//       }
		//   }
		log.Println("html prewarm: complete (add your own pre-warm queries)")
	}()
}

// ── SPA handler ────────────────────────────────────────────────────────────

// makeSPAHandler serves the embedded React SPA.
// - /assets/* → immutable cache headers (Vite adds content hashes to filenames)
// - Known files → serve with 1-day cache
// - Unknown paths → return index.html for client-side routing
//
// The root "/" path triggers SSR + caching. Customize the SSR logic here
// to match your home page data needs.
func makeSPAHandler(sub fs.FS, indexHTML []byte) echo.HandlerFunc {
	fileServer := http.FileServer(http.FS(sub))
	return func(c echo.Context) error {
		p := strings.TrimLeft(c.Request().URL.Path, "/")

		// Root path: attempt SSR for the home page
		if p == "" {
			c.Response().Header().Set("Cache-Control", publicHTMLCacheCC)
			const cacheKey = "home/"
			if cached, ok, stale := getCachedHTMLState(cacheKey); ok {
				if stale {
					// Serve stale, refresh in background
					refreshCachedHTML(cacheKey, func() ([]byte, error) {
						// CUSTOMIZE: add your home page data fetch here
						ssrHTML, err := callSSR("/", "", nil)
						if err != nil {
							return nil, err
						}
						return injectSSRContent(indexHTML, ssrHTML, "", nil), nil
					}, htmlStaticTTL, htmlStaticSWR)
				}
				return c.HTMLBlob(http.StatusOK, cached)
			}
			// Cache miss: render now
			if ssrHTML, err := callSSR("/", "", nil); err == nil {
				out := injectSSRContent(indexHTML, ssrHTML, "", nil)
				setStaticHTML(cacheKey, out)
				return c.HTMLBlob(http.StatusOK, out)
			}
			// SSR unavailable: serve bare index.html (CSR fallback)
			return c.HTMLBlob(http.StatusOK, indexHTML)
		}

		// Try to serve a static file
		f, err := sub.Open(p)
		if err != nil {
			// Not a static file → SPA catch-all
			c.Response().Header().Set("Cache-Control", "no-cache")
			return c.HTMLBlob(http.StatusOK, indexHTML)
		}
		info, err := f.Stat()
		f.Close()
		if err != nil || info.IsDir() {
			c.Response().Header().Set("Cache-Control", "no-cache")
			return c.HTMLBlob(http.StatusOK, indexHTML)
		}

		// Vite puts content-hashed files in /assets/ — safe to cache forever.
		if strings.HasPrefix(p, "assets/") {
			c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			c.Response().Header().Set("Cache-Control", "public, max-age=86400")
		}
		fileServer.ServeHTTP(c.Response(), c.Request())
		return nil
	}
}

// ── Main ───────────────────────────────────────────────────────────────────

func main() {
	// Start the Node SSR sidecar. The circuit breaker starts at 0 (closed) until
	// the readiness poll succeeds. Early requests fall back to CSR automatically.
	startSSRServer()

	// Load and optimize the embedded index.html once at startup.
	sub, err := fs.Sub(appStatic.FS, "dist")
	if err != nil {
		log.Fatalf("static fs: %v", err)
	}
	indexHTML, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		log.Fatal("static/dist/index.html not found — run `npm run build` in frontend/ first")
	}
	indexHTML = optimizeIndexHTML(indexHTML)

	// Pre-warm the HTML cache in the background.
	prewarmHTMLCache(context.Background(), indexHTML)

	e := echo.New()
	e.HideBanner = true
	e.Use(echoMiddleware.Logger())
	e.Use(echoMiddleware.Recover())
	e.Use(echoMiddleware.SecureWithConfig(echoMiddleware.SecureConfig{
		XSSProtection:      "1; mode=block",
		ContentTypeNosniff: "nosniff",
		XFrameOptions:      "SAMEORIGIN",
	}))
	e.Use(echoMiddleware.GzipWithConfig(echoMiddleware.GzipConfig{
		Level: 5,
		Skipper: func(c echo.Context) bool {
			// Skip compression for proxied API responses — the API already compresses
			return strings.HasPrefix(c.Request().URL.Path, "/api/")
		},
	}))

	// Health check — used by Railway / Docker health checks
	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// ── Content detail pages ──────────────────────────────────────────────
	//
	// CUSTOMIZE: Replace this example with your own content routes.
	// The pattern for any SSR-eligible route:
	//   1. Check cache (getCachedHTMLState)
	//   2. Handle crawler UA separately (serveOG)
	//   3. Fetch data from DB
	//   4. callSSR(path, coverImage, initialData)
	//   5. injectSSRContent(indexHTML, ssrHTML, coverImage, initialData)
	//   6. Cache the result (setStaticHTML / setCachedHTML)
	//   7. Fallback: injectCoverPreload(indexHTML, coverImage) if SSR fails
	//
	// IMPORTANT: Only SSR routes that are:
	//   (a) publicly accessible without authentication
	//   (b) have the same content for every visitor
	// Authenticated routes must serve plain indexHTML — SSR would cache
	// the anonymous HTML and serve it to logged-in users.
	// See docs/01-ssr-trigger-routes.md.
	e.GET("/content/:id/:slug", func(c echo.Context) error {
		id := c.Param("id")
		cacheKey := c.Request().URL.RequestURI()

		// CUSTOMIZE: skip SSR for reserved slugs that route to auth-gated pages
		switch c.Param("slug") {
		case "edit", "manage", "settings":
			c.Response().Header().Set("Cache-Control", "no-cache")
			return c.HTMLBlob(http.StatusOK, indexHTML)
		}

		if isCrawler(c.Request().Header.Get("User-Agent")) {
			// CUSTOMIZE: Fetch your content from DB and populate ogData
			c.Response().Header().Set("Cache-Control", "no-store")
			return serveOG(c, ogData{
				Title:       "Content " + id,
				Description: "Content description",
				Image:       siteBase + "/og-default.png",
				URL:         siteBase + c.Request().URL.RequestURI(),
			})
		}

		if cached, ok, stale := getCachedHTMLState(cacheKey); ok {
			c.Response().Header().Set("Cache-Control", publicHTMLCacheCC)
			if stale {
				// Serve stale now, re-render in background
				refreshCachedHTML(cacheKey, func() ([]byte, error) {
					// CUSTOMIZE: fetch your data here
					ssrHTML, err := callSSR(cacheKey, "", nil)
					if err != nil {
						return nil, err
					}
					return injectSSRContent(indexHTML, ssrHTML, "", nil), nil
				}, htmlStaticTTL, htmlStaticSWR)
			}
			return c.HTMLBlob(http.StatusOK, cached)
		}

		c.Response().Header().Set("Cache-Control", publicHTMLCacheCC)

		// CUSTOMIZE: fetch content from your DB, populate initialData
		// Example:
		//   item, err := db.GetContent(c.Request().Context(), id)
		//   if err != nil { return c.HTMLBlob(http.StatusOK, indexHTML) }
		//   coverImage := item.CoverImageURL
		//   initialData := buildInitialData(item)

		coverImage := "" // CUSTOMIZE: set from DB query
		var initialData any = nil // CUSTOMIZE: set from DB query

		if ssrHTML, err := callSSR(cacheKey, coverImage, initialData); err == nil {
			out := injectSSRContent(indexHTML, ssrHTML, coverImage, initialData)
			setStaticHTML(cacheKey, out)
			return c.HTMLBlob(http.StatusOK, out)
		}
		// SSR failed — serve with cover preload as a lighter optimization
		out := injectCoverPreload(indexHTML, coverImage)
		setCachedHTML(cacheKey, out)
		return c.HTMLBlob(http.StatusOK, out)
	})

	// ── API proxy ─────────────────────────────────────────────────────────
	//
	// Proxy /api/* to the API service. This is optional — remove if your
	// Go binary also serves the API directly (monolith mode).
	//
	// ModifyResponse intercepts X-App-Invalidate headers from the API to
	// invalidate HTML cache entries when content is updated.
	//
	// CUSTOMIZE: Set SOGS_API_PRIVATE_URL to the internal Railway URL (private
	// networking) to avoid routing through the public internet.
	apiTarget := os.Getenv("API_PRIVATE_URL")
	if apiTarget == "" {
		apiTarget = os.Getenv("API_URL")
	}
	if apiTarget == "" {
		apiTarget = "http://localhost:8080"
	}
	apiProxyURL, err := url.Parse(apiTarget)
	if err != nil {
		log.Fatalf("invalid API proxy URL: %v", err)
	}
	apiProxy := httputil.NewSingleHostReverseProxy(apiProxyURL)
	origDirector := apiProxy.Director
	apiProxy.Director = func(req *http.Request) {
		origHost := req.Host
		origDirector(req)
		req.Host = origHost // preserve original Host for OAuth redirect URIs, etc.
	}
	// CUSTOMIZE: Update the header name to match your API's invalidation header.
	// Your API write handlers should set X-App-Invalidate: <content-id>
	// This proxy middleware then evicts those cache entries immediately.
	apiProxy.ModifyResponse = func(resp *http.Response) error {
		if idHeader := resp.Header.Get("X-App-Invalidate"); idHeader != "" {
			for _, id := range strings.Split(idHeader, ",") {
				invalidateContentHTML(strings.TrimSpace(id))
			}
			resp.Header.Del("X-App-Invalidate")
		}
		return nil
	}
	e.Any("/api/*", func(c echo.Context) error {
		apiProxy.ServeHTTP(c.Response(), c.Request())
		return nil
	})

	// SPA catch-all — must be last
	e.GET("/*", makeSPAHandler(sub, indexHTML))

	port := getEnvOrDefault("PORT", "3000")
	log.Printf("web: listening on :%s", port)
	if err := e.Start(":" + port); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
