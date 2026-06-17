import {
  createRouter,
  createRootRoute,
  createRoute,
  Outlet,
} from '@tanstack/react-router'

// CUSTOMIZE: Replace these placeholder routes with your real routes.
// createAppRouter() is called:
//   - Once in App.tsx for the client-side singleton
//   - Once per request in entry-server.tsx (with a memory history) for SSR
// Both must use the same route tree so server and client render identically.

const rootRoute = createRootRoute({
  component: Outlet,
})

// CUSTOMIZE: Add your routes here. Each route component can access server
// data via useSSRContext().initialData — see docs/02-initialdata-contract.md.
const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: () => {
    // CUSTOMIZE: Import and use your real home page component
    return null
  },
})

// Example content detail route — CUSTOMIZE with your own URL pattern and component
const contentDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/content/$id/$slug',
  component: () => {
    // CUSTOMIZE: Import and use your real content detail component
    return null
  },
})

const routeTree = rootRoute.addChildren([indexRoute, contentDetailRoute])

/**
 * createAppRouter creates the TanStack Router instance.
 * Accepts optional options to override history (used in SSR for memory history).
 *
 * If you prefer React Router v6/v7, replace this function body with:
 *   import { createStaticRouter, StaticHandlerContext } from 'react-router-dom/server'
 *   // and update entry-server.tsx to use createStaticHandler + createStaticRouter
 *   // See docs/01-ssr-trigger-routes.md for the React Router equivalent pattern.
 */
export function createAppRouter(opts?: Parameters<typeof createRouter>[0]) {
  return createRouter({ routeTree, ...opts })
}

export type AppRouter = ReturnType<typeof createAppRouter>
