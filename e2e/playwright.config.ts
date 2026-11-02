import { defineConfig, devices } from "@playwright/test"

// E2E_BASE_URL is the origin of the running app. The Docker image (and the
// backend with a built frontend) serves the SPA, /api/... and /health on one
// origin, so the default http://localhost:8080 covers every spec. With the dev
// stack use the Vite origin instead (it proxies /api; /health is then not
// reachable there, so health.spec.ts needs the backend origin).
// No webServer block: the app is started by the caller (make dev, docker, ...).
const baseURL = process.env.E2E_BASE_URL ?? "http://localhost:8080"

export default defineConfig({
  testDir: "./tests",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.E2E_WORKERS ? Number(process.env.E2E_WORKERS) : undefined,
  reporter: [["list"], ["html", { open: "never", outputFolder: "playwright-report" }]],
  use: {
    baseURL,
    viewport: { width: 1280, height: 800 },
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
})
