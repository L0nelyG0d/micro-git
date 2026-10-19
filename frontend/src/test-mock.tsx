// Mock-service helpers for screen tests (#102). Kept apart from test-utils.tsx so
// the many test files that only need MemoryRouter or nextTask do not import the
// whole App and the mock (that import cost was ~7% of the suite's CPU time).
import type { ReactNode } from "react"
import { render } from "@testing-library/react"
import type { createMemoryRouter } from "react-router-dom"
import { afterEach } from "vitest"
import App from "@/App"
import { cacheClear } from "@/lib/fetchCache"
import { MockApiService } from "@/services/MockApiService"
import { setServiceOverride } from "@/test-service"
import { MemoryRouter } from "@/test-utils"

afterEach(() => setServiceOverride(null))

/** Makes the app's `apiService` a (new) MockApiService for the current test, without rendering anything. */
export function installMock(mock = new MockApiService()) {
  cacheClear()
  setServiceOverride(mock)
  return mock
}

/**
 * Renders the whole app (App layout + routes) at `path` on top of a fresh
 * MockApiService, with the service swapped through the `@/services` mock in
 * test-setup.ts. Returns the mock so a test can seed data or call its methods
 * (e.g. logout() to expire the session) on the same instance the screens use.
 * `nav.history` are entries before `path` (for Back) and `nav.routerRef` receives the router. `extra` renders next to the app inside the router. The mock is dropped after each test.
 */
export function renderWithMock(
  path = "/",
  mock = new MockApiService(),
  extra?: ReactNode,
  nav: { history?: string[]; routerRef?: (r: ReturnType<typeof createMemoryRouter>) => void } = {},
) {
  installMock(mock)
  const view = render(
    <MemoryRouter
      initialEntries={[...(nav.history ?? []), path]}
      initialIndex={nav.history?.length ?? 0}
      routerRef={nav.routerRef}
    >
      <App />
      {extra}
    </MemoryRouter>,
  )
  return { mock, ...view }
}
