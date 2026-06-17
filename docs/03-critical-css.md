# Critical CSS

## The problem

When the browser parses an HTML document and finds a `<link rel="stylesheet">`, it stops rendering until the stylesheet downloads. On a 3G connection or high-latency mobile network, this adds 500-1500ms to First Contentful Paint (FCP).

Vite generates a single hashed stylesheet (e.g. `assets/index-aBcDeF12.css`) that contains all your styles. Until it loads, the page is blank.

## The solution

Two complementary techniques:

### 1. Inline critical CSS

Extract the CSS needed to render the above-fold content (the visible portion before scrolling) and inline it directly in `<style>` in `<head>`. The browser can paint immediately without waiting for any network request.

In `cmd/web/main.go`, the `optimizeIndexHTML()` function injects `criticalCSS` just before `</head>` on startup.

### 2. Async-load the full stylesheet

Convert the Vite CSS `<link rel="stylesheet">` to a preload that converts to stylesheet after paint:

```html
<!-- Before (render-blocking) -->
<link rel="stylesheet" href="/assets/index-aBcDeF12.css">

<!-- After (async, with noscript fallback) -->
<link rel="preload" as="style" onload="this.onload=null;this.rel='stylesheet'" href="/assets/index-aBcDeF12.css">
<noscript><link rel="stylesheet" href="/assets/index-aBcDeF12.css"></noscript>
```

The `stylesheetLinkRE` regex in `optimizeIndexHTML()` handles this transformation automatically.

## How to extract your critical CSS

### Method 1: Chrome Coverage tab

1. Open Chrome DevTools → More tools → Coverage
2. Click the record button and load your page
3. Stop recording
4. Look at your CSS file — the green sections are used, red are unused
5. Copy the used-on-first-paint styles into the `criticalCSS` constant

### Method 2: Lighthouse

1. Run `npx lighthouse https://your-app.com --view`
2. Look for "Eliminate render-blocking resources" in the report
3. The linked CSS coverage shows exactly which selectors are above-fold

### Method 3: Critical npm package

```bash
npx critical index.html --base dist/ --inline --width 1300 --height 900
```

## What to include

- Reset styles (`box-sizing`, `margin: 0` on body)
- Font declarations (`@font-face` if self-hosting fonts)
- Typography for above-fold headings and body text
- Layout classes used by the header/nav
- Cover image container dimensions (prevents layout shift)
- Loading skeleton styles if you show them server-side

## What to exclude

- Component styles that are only visible after scrolling
- Animation keyframes (not needed before interaction)
- Dark mode variants (can load asynchronously)
- Print styles

## Maintenance

The critical CSS blob goes stale as your design changes. A few strategies:

1. **Add a comment** in `main.go` noting when it was last updated and from which Lighthouse run
2. **CI regression**: run Lighthouse in CI and fail if FCP degrades > 200ms vs. baseline
3. **Tailwind layers**: if using Tailwind, use `@layer` to split critical from non-critical utilities:

```css
@layer base, critical, components, utilities;

@layer critical {
  /* above-fold classes only */
  .hero { ... }
  .nav { ... }
}
```

## With Tailwind v4

Tailwind v4's `@tailwindcss/vite` generates only the classes you use. The critical CSS blob will be smaller and easier to maintain. Extract it the same way — run Coverage on your homepage and copy the above-fold subset.
