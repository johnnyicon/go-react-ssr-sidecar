import { Suspense } from 'react'
import { RouterProvider } from '@tanstack/react-router'
import { createAppRouter } from './router'

// Client singleton router — created once for the lifetime of the browser session.
// For SSR, entry-server.tsx creates a new router per request with a memory history.
// Exported so it can be passed to router tracing integrations (e.g. Sentry).
export const router = createAppRouter()

function App() {
  return (
    <Suspense fallback={null}>
      <RouterProvider router={router} />
    </Suspense>
  )
}

export default App
