import { MemoryRouter } from "@/test-utils"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Link } from "react-router-dom"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import App from "@/App"
import { pageRoutes } from "@/routes"
import { cacheClear } from "@/lib/fetchCache"

// The routes module is real; the Home page is swapped for one that can throw.
const state = { fail: true }
vi.mock("@/pages/Home", () => ({
  Home: () => {
    if (state.fail) throw new Error("boom")
    return (
      <div>
        <h1>Home ok</h1>
        <Link to="/somewhere/else">go</Link>
      </div>
    )
  },
}))

beforeEach(() => {
  cacheClear()
  state.fail = true
  vi.spyOn(console, "error").mockImplementation(() => {})
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      const ok = (body: unknown, status = 200) =>
        ({ status, ok: status < 300, json: async () => body, headers: new Headers() }) as Response
      return url === "/api/auth/me" ? ok({ error: "authentication required" }, 401) : ok({})
    }),
  )
})
afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  )
}

it("shows the fallback inside the layout when a page throws", async () => {
  renderAt("/")
  const alert = await screen.findByRole("alert")
  expect(alert).toHaveTextContent("Something went wrong")
  expect(alert).toHaveTextContent("Check your connection and try again or go back to repositories")
  expect(screen.getByRole("navigation")).toBeInTheDocument()
  expect(screen.getByRole("link", { name: "Back to repositories" })).toHaveAttribute("href", "/")
})

it("Retry renders the page again, and the fallback returns if it throws again", async () => {
  renderAt("/")
  await screen.findByRole("alert")
  await userEvent.click(screen.getByRole("button", { name: "Retry" }))
  expect(await screen.findByRole("alert")).toHaveTextContent("Something went wrong")
  state.fail = false
  await userEvent.click(screen.getByRole("button", { name: "Retry" }))
  expect(await screen.findByRole("heading", { name: "Home ok" })).toBeInTheDocument()
  expect(screen.queryByRole("alert")).not.toBeInTheDocument()
})

it("navigating to another route clears the fallback without a reload", async () => {
  renderAt("/")
  await screen.findByRole("alert")
  await userEvent.click(screen.getByRole("link", { name: /log in/i }))
  expect(await screen.findByRole("heading", { name: "Log in" })).toBeInTheDocument()
  expect(screen.queryByText("Something went wrong")).not.toBeInTheDocument()
})

it("a lazy page whose import rejects shows the same fallback", async () => {
  const Lazy = (await import("react")).lazy(() => Promise.reject(new Error("chunk failed")))
  pageRoutes.unshift({ path: "/broken", element: <Lazy /> })
  try {
    renderAt("/broken")
    expect(await screen.findByRole("alert")).toHaveTextContent("Something went wrong")
  } finally {
    pageRoutes.shift()
  }
})
