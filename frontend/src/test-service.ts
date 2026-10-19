import type { ApiService } from "@/services/ApiService"

/**
 * Holder for the service the app uses in a test. test-setup.ts mocks `@/services`
 * so `apiService` forwards to `current()`; renderWithMock() (test-utils.tsx) sets it.
 * Outside renderWithMock the real HttpApiService answers (tests stub fetch).
 */
let override: ApiService | null = null
export const setServiceOverride = (s: ApiService | null) => {
  override = s
}
export const serviceOverride = () => override
