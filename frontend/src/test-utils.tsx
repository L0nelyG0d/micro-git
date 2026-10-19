import { useState } from "react"
import type { ReactNode } from "react"
import { pageRoutes } from "@/routes"
import App from "@/App"
import { cacheClear } from "@/lib/fetchCache"
import { MockApiService } from "@/services/MockApiService"
import { setServiceOverride } from "@/test-service"
import { render } from "@testing-library/react"
import { afterEach } from "vitest"
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

afterEach(() => setServiceOverride(null))

/**
 * Renders the whole app (App layout + routes) at `path` on top of a fresh
 * MockApiService, with the service swapped through the `@/services` mock in
 * test-setup.ts. Returns the mock so a test can seed data or call its methods
 * (e.g. logout() to expire the session) on the same instance the screens use.
 * `extra` renders next to the app inside the router. The mock is dropped after each test.
 */
export function renderWithMock(path = "/", mock = new MockApiService(), extra?: ReactNode) {
  cacheClear()
  setServiceOverride(mock)
  const view = render(
    <MemoryRouter initialEntries={[path]}>
      <App />
      {extra}
    </MemoryRouter>,
  )
  return { mock, ...view }
}
