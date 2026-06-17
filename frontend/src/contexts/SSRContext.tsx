import { createContext, useContext } from 'react'

/**
 * SSRContext carries server-injected data into the initial React render.
 *
 * On the server (entry-server.tsx):
 *   SSRProvider is populated from the callSSR() request payload (coverImage + initialData).
 *
 * On the client (main.tsx):
 *   SSRProvider is populated from window.__APP_DATA__ — the same values that
 *   Go injected via ssrBootScript() — so hydrateRoot() sees matching props.
 *
 * The initialData field is the cross-language contract between Go and React.
 * CUSTOMIZE: Replace `unknown` with your specific TypeScript interface.
 * See docs/02-initialdata-contract.md for the full contract specification.
 *
 * Example:
 *   interface PostInitialData {
 *     id: string
 *     title: string
 *     coverImageUrl: string | null
 *     body: string
 *   }
 *   const SSRContext = createContext<SSRContextValue<PostInitialData>>({ ... })
 */

interface SSRContextValue {
  coverImage: string | null
  // CUSTOMIZE: Replace `unknown` with your initialData interface.
  // The shape must exactly match the Go struct serialized in cmd/web/main.go.
  // There is no compile-time enforcement across the Go↔React boundary —
  // see docs/02-initialdata-contract.md for how to catch mismatches.
  initialData: unknown | null
}

const SSRContext = createContext<SSRContextValue>({
  coverImage: null,
  initialData: null,
})

export function SSRProvider({
  children,
  value,
}: {
  children: React.ReactNode
  value: SSRContextValue
}) {
  return <SSRContext.Provider value={value}>{children}</SSRContext.Provider>
}

/**
 * useSSRContext returns the server-injected data for the current page.
 *
 * Usage in a component:
 *   const { coverImage, initialData } = useSSRContext()
 *   const data = initialData as MyContentType
 *
 * initialData is available immediately on the first render (both SSR and
 * hydration), before any API calls complete. This is what eliminates the
 * "loading..." flash on SSR pages.
 */
export function useSSRContext(): SSRContextValue {
  return useContext(SSRContext)
}
