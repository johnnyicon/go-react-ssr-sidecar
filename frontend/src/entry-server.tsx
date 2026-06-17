import React, { Suspense } from 'react'
import { renderToString } from 'react-dom/server'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { SSRProvider } from './contexts/SSRContext'
import { createAppRouter } from './router'

/**
 * render is called by ssr-server.mjs once per HTTP request.
 *
 * Parameters:
 *   url         — the path+query string the browser requested
 *                 (e.g. "/content/abc123/my-title")
 *   coverImage  — the cover/hero image URL from Go's DB query, or null
 *   initialData — route-specific data from Go, matching the React
 *                 initialData interface. See docs/02-initialdata-contract.md.
 *
 * Returns the HTML string for the React component tree.
 * This is the content that goes inside <div id="root"> — not a full document.
 *
 * The same coverImage and initialData values MUST be passed to SSRProvider
 * on the client (in main.tsx via window.__APP_DATA__) so that hydrateRoot()
 * sees identical props on first render. Any difference causes React error #418
 * (hydration mismatch).
 */
export async function render(
  url: string,
  coverImage: string | null,
  initialData: unknown = null,
): Promise<string> {
  const memHistory = createMemoryHistory({ initialEntries: [url] })
  const router = createAppRouter({ history: memHistory })

  // Pre-load the matched route: resolves route params, runs any route loaders.
  // Must complete before renderToString() so loader data is available to components.
  await router.load()

  return renderToString(
    <SSRProvider value={{ coverImage, initialData }}>
      <Suspense fallback={null}>
        <RouterProvider router={router} />
      </Suspense>
    </SSRProvider>,
  )
}
