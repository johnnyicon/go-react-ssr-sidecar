import { StrictMode } from 'react'
import { createRoot, hydrateRoot } from 'react-dom/client'
import './index.css'
import App, { router } from './App'
import { SSRProvider } from './contexts/SSRContext'

/**
 * Client entry point.
 *
 * On pages where Go performed SSR, the HTML already has:
 *   1. React component tree pre-rendered in #root
 *   2. window.__APP_DATA__ set with { coverImage, initialData }
 *
 * We read __APP_DATA__ here and pass the same values to SSRProvider so that
 * hydrateRoot() sees matching props and can adopt the server-rendered DOM
 * without re-rendering (the "hydration" fast path).
 *
 * On CSR-only pages (authenticated routes, pages where SSR was skipped),
 * #root is empty and __APP_DATA__ is not set. We fall back to createRoot()
 * for a normal client-side render.
 *
 * CUSTOMIZE: The global name "__APP_DATA__" must match:
 *   - cmd/web/main.go ssrBootScript() function
 *   - This file (window.__APP_DATA__ read)
 *   - frontend/src/contexts/SSRContext.tsx (type declaration)
 */

// Read server-injected data. Must match exactly what was passed to callSSR()
// in cmd/web/main.go — any difference causes React hydration warnings.
const ssrPayload =
  (window as unknown as {
    __APP_DATA__?: { coverImage?: string | null; initialData?: unknown }
  }).__APP_DATA__ ?? {}
const ssrCoverImage = ssrPayload.coverImage ?? null
const ssrInitialData = ssrPayload.initialData ?? null

const app = (
  <StrictMode>
    <SSRProvider value={{ coverImage: ssrCoverImage, initialData: ssrInitialData }}>
      <App />
    </SSRProvider>
  </StrictMode>
)

const root = document.getElementById('root')!

// If the server pre-rendered HTML into #root, use hydrateRoot to adopt the
// existing DOM (fast, no re-render). Otherwise do a normal createRoot render.
if (root.hasChildNodes()) {
  hydrateRoot(root, app)
} else {
  createRoot(root).render(app)
}
