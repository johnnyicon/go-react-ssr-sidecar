# LCP Optimization

Largest Contentful Paint (LCP) measures when the largest visible element in the viewport becomes visible. For content apps, this is almost always the cover/hero image. This document covers all the optimization techniques baked into this template.

## 1. Server-side cover image preload injection

Go knows the cover image URL at the time it constructs the HTML response (from the DB query). It injects `<link rel="preload" as="image" fetchpriority="high">` directly into `<head>` before sending the response.

This means the browser starts fetching the cover image in parallel with JavaScript bundles — before React has executed even a single line of code. Without this, the image doesn't start loading until React renders the component that contains the `<img>` tag, which is typically 2-5 seconds after navigation on mobile.

Implementation in `cmd/web/main.go`:

```go
// injectSSRContent adds preload hints before the boot script
mobile := `<link rel="preload" as="image" href="` + html.EscapeString(mobileURL) + `" media="(max-width: 768px)" fetchpriority="high">`
desktop := `<link rel="preload" as="image" href="` + html.EscapeString(desktopURL) + `" media="(min-width: 769px)" fetchpriority="high">`
```

Using separate `media` queries for mobile/desktop prevents the browser from downloading both variants when only one is needed.

**LCP improvement: typically 3-6 seconds on mobile 3G.**

## 2. SSR: hero image in the initial HTML

With SSR enabled, the `<img>` tag for the cover image is in the initial HTML response (inside `<div id="root">`). The browser's preload scanner can discover it immediately, even before JavaScript executes.

Without SSR, the `<img>` tag doesn't exist until React renders — typically several seconds after page load on mobile.

**LCP improvement: 2-5 seconds on mobile. The biggest single optimization in this template.**

## 3. IntersectionObserver for below-fold images

For images that are below the fold (not visible on initial load), use `loading="lazy"` on the `<img>` tag. This ensures they never become LCP candidates under Lighthouse's headless Chrome (which uses a viewport of 800x600 and doesn't scroll).

```tsx
// Above-fold: eager load + fetchpriority
<img src={item.coverUrl} loading="eager" fetchPriority="high" />

// Below-fold: lazy load
<img src={item.thumbnailUrl} loading="lazy" />
```

For more control, use IntersectionObserver to trigger loading only when the element enters the viewport:

```tsx
const ref = useRef<HTMLDivElement>(null)
const [visible, setVisible] = useState(false)

useEffect(() => {
  const observer = new IntersectionObserver(
    ([entry]) => { if (entry.isIntersecting) setVisible(true) },
    { rootMargin: '200px' } // start loading 200px before entering viewport
  )
  if (ref.current) observer.observe(ref.current)
  return () => observer.disconnect()
}, [])
```

## 4. Client-side WebP compression before upload

When users upload cover images, compress and resize them in the browser before sending to your API/storage. This reduces storage costs and ensures your cover images are always the right size.

```typescript
async function compressImage(file: File): Promise<Blob> {
  const MAX_WIDTH = 1920
  const QUALITY = 0.82 // 82% quality — good balance of size and clarity

  return new Promise((resolve, reject) => {
    const img = new Image()
    img.onload = () => {
      const canvas = document.createElement('canvas')
      const ratio = Math.min(1, MAX_WIDTH / img.width)
      canvas.width = Math.round(img.width * ratio)
      canvas.height = Math.round(img.height * ratio)

      const ctx = canvas.getContext('2d')!
      ctx.drawImage(img, 0, 0, canvas.width, canvas.height)

      canvas.toBlob(
        (blob) => blob ? resolve(blob) : reject(new Error('Compression failed')),
        'image/webp',
        QUALITY,
      )
    }
    img.onerror = reject
    img.src = URL.createObjectURL(file)
  })
}
```

Also generate an 800w mobile variant for srcSet:

```typescript
async function compressMobileVariant(file: File): Promise<Blob> {
  // Same as above but MAX_WIDTH = 800
}
```

## 5. Cloudflare edge cache

Cloudflare caches SSR HTML at edge nodes globally. TTFB drops from ~500ms (Railway origin round-trip) to ~30-80ms (Cloudflare edge).

- First visitor: 500ms TTFB (origin hit, response cached at edge)
- Subsequent visitors (same Cloudflare region): ~50ms TTFB (edge cache hit)

See `docs/05-cloudflare-rules.md` for setup.

## 6. CDN preconnect hints

Add `<link rel="preconnect">` for every external domain your app fetches images from. This establishes the TCP+TLS handshake before the browser knows it needs a resource.

In `frontend/index.html`:

```html
<!-- Your image CDN (R2, S3, Cloudinary, etc.) -->
<link rel="preconnect" href="https://your-images.r2.dev" />
<!-- External fonts if self-hosting isn't an option -->
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
```

**Important:** Only add preconnect for domains that will definitely be needed on page load. Too many preconnects compete for TCP connections and can hurt rather than help.

## 7. Critical CSS inlining

See `docs/03-critical-css.md`. Inlining above-fold CSS eliminates one render-blocking network request, improving FCP by 200-500ms.

## 8. Analytics deferred to `window.load`

Analytics scripts add kilobytes of JavaScript to the critical path. Move them out of `<head>` and initialize them after the page is fully loaded:

```html
<!-- In index.html, AFTER the React app script -->
<script>
  window.addEventListener('load', function() {
    // Initialize analytics here — fires after all resources have loaded
    // and React is fully hydrated
  });
</script>
```

This ensures analytics never compete with React or the cover image for bandwidth.

## Measuring LCP

1. **Lighthouse CLI** (most reliable — headless Chrome, reproducible):
   ```bash
   npx lighthouse https://your-app.com --only-metrics=largest-contentful-paint --view
   ```

2. **Chrome DevTools Performance tab** — record a page load with CPU throttling 4x, network throttling Slow 3G. Look for the LCP marker.

3. **WebPageTest** (https://webpagetest.org) — tests from real locations on real devices. The waterfall view shows exactly what loaded when.

## Target LCP values

- < 2.5s: Good (Google green)
- 2.5s – 4.0s: Needs improvement
- > 4.0s: Poor (Google red, may affect rankings)

On mobile 3G, most content apps without SSR land at 4-8s LCP. With this template's optimizations (SSR + preload injection + critical CSS + Cloudflare), 1.5-2.5s is achievable.
