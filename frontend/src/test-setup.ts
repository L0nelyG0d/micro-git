import { vi } from "vitest"
import "@testing-library/jest-dom/vitest"

// jsdom has no matchMedia (sonner reads it).
if (!window.matchMedia) {
  window.matchMedia = (query: string) =>
    ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }) as MediaQueryList
}

// jsdom lacks the pointer-capture and scroll APIs Radix Select uses.
if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false
  Element.prototype.setPointerCapture = () => {}
  Element.prototype.releasePointerCapture = () => {}
}
if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {}

// The one place the service is swapped (#102). `@/services` exports a module
// singleton, so its `apiService` is replaced by a forwarding proxy: it uses the
// MockApiService set by renderWithMock() (test-mock.tsx) and otherwise the real
// HttpApiService, which keeps the fetch-stub tests unchanged.
vi.mock("@/services", async (importOriginal) => {
  const real = await importOriginal<typeof import("@/services")>()
  const { serviceOverride } = await import("@/test-service")
  const apiService = new Proxy(real.apiService, {
    get: (target, key) => {
      const s = serviceOverride() ?? target
      const v = Reflect.get(s, key)
      return typeof v === "function" ? v.bind(s) : v
    },
  })
  return { ...real, apiService }
})
