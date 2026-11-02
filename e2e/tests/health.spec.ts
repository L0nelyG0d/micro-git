import { expect, test } from "@playwright/test"

// Needs the backend origin (E2E_BASE_URL, default http://localhost:8080): the Vite
// proxy only forwards /api, so /health is answered by the Go server itself.

test("GET /health answers 200 OK", async ({ request }) => {
  const res = await request.get("/health")
  expect(res.status()).toBe(200)
  expect((await res.text()).trim()).toBe("OK")
})

test("HEAD /health answers 200 with no body", async ({ request }) => {
  const res = await request.head("/health")
  expect(res.status()).toBe(200)
})
