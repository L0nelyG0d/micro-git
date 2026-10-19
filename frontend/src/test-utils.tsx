import { useState } from "react"
import type { ReactNode } from "react"
import { pageRoutes } from "@/routes"
import { createMemoryRouter, RouterProvider } from "react-router-dom"
/**
 * Resolves on the next macrotask. Fetch mocks await it before answering so a
 * page's transient "Loading ..." state stays in the DOM until Testing Library's
 * MutationObserver (a microtask) has seen it. Without it the mock settles in
 * microtasks, and since routes are React.lazy (#52) the loading state can appear
 * and vanish between two findBy checks when the machine is busy.
 */
export const nextTask = () => new Promise<void>((resolve) => setTimeout(resolve, 0))

/**
 * Drop-in for MemoryRouter that creates a data router (createMemoryRouter), as
 * main.tsx does in production, so components using useBlocker work in tests.
 */
export function MemoryRouter({
  initialEntries,
  initialIndex,
  routerRef,
  children,
}: {
  initialEntries?: string[]
  initialIndex?: number
  /** Receives the router, e.g. to call router.navigate(-1) (a history pop, like the Back button). */
  routerRef?: (router: ReturnType<typeof createMemoryRouter>) => void
  children?: ReactNode
}) {
  const [router] = useState(() => {
    const r = createMemoryRouter([{ element: children, children: pageRoutes }], {
      initialEntries,
      initialIndex,
    })
    routerRef?.(r)
    return r
  })
  return <RouterProvider router={router} />
}
