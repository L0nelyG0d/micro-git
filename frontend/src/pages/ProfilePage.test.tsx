import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import App from "@/App"
import { cacheClear } from "@/lib/fetchCache"
import { MemoryRouter, nextTask, renderWithMock } from "@/test-utils"
import { MockApiService } from "@/services/MockApiService"

const PW = "password1"
/** A mock where alice owns `repos` and nobody is logged in. */
async function withRepos(repos: { name: string; private?: boolean }[]) {
  const mock = new MockApiService()
  await mock.register("alice", PW)
  await mock.login("alice", PW)
  for (const r of repos) await mock.createRepo(r)
  await mock.logout()
  return mock
}

// Starting point for the ProfilePage suite (#56): extend here.
let routes: Record<string, () => { status: number; body?: unknown }>

beforeEach(() => {
  cacheClear()
  routes = {}
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const h = routes[`${init?.method ?? "GET"} ${url}`]
      if (!h) throw new Error(`unexpected fetch ${init?.method ?? "GET"} ${url}`)
      if (url.startsWith("/api/repos")) await nextTask() // keep the loading state observable
      const r = h()
      return { status: r.status, ok: r.status < 300, json: async () => r.body } as Response
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

it("encodes the owner in the pagination links", async () => {
  routes["GET /api/auth/me"] = () => ({ status: 401, body: { error: "authentication required" } })
  routes["GET /api/repos?owner=a%20b&page=2"] = () => ({
    status: 200,
    body: { repos: [{ id: 1, owner: "a b", name: "x", description: "", default_branch: "main", empty: true, created_at: "2026-01-02T15:04:05Z" }], page: 2, per_page: 50, has_more: true },
  })
  render(
    <MemoryRouter initialEntries={["/a%20b?page=2"]}>
      <App />
    </MemoryRouter>,
  )
  const nav = await screen.findByRole("navigation", { name: "Pagination" })
  expect(within(nav).getByRole("link", { name: "Next" })).toHaveAttribute("href", "/a%20b?page=3")
  expect(within(nav).getByRole("link", { name: "Previous" })).toHaveAttribute("href", "/a%20b")
})

// --- #56: states, owner-only action, badges ---
const me = (username: string) => () => ({ status: 200, body: { id: 1, username } })
const loggedOut = () => ({ status: 401, body: { error: "authentication required" } })
const aliceRepos = (repos: unknown[]) => () => ({ status: 200, body: { repos, page: 1, per_page: 50, has_more: false } })
const aRepo = (o: Record<string, unknown> = {}) => ({
  id: 1, owner: "alice", name: "hello", description: "", default_branch: "main", empty: true, private: false,
  created_at: "2026-01-02T15:04:05Z", ...o,
})
const renderProfile = () =>
  render(
    <MemoryRouter initialEntries={["/alice"]}>
      <App />
    </MemoryRouter>,
  )

it("shows a loading state while the list is pending", async () => {
  routes["GET /api/auth/me"] = loggedOut
  routes["GET /api/repos?owner=alice&page=1"] = () => ({ status: 200, body: { repos: [], page: 1, per_page: 50, has_more: false } })
  renderProfile()
  expect(await screen.findByRole("status", { name: "Loading repositories" })).toBeInTheDocument()
  expect(await screen.findByText("alice has no repositories you can see.")).toBeInTheDocument()
})

it("says so when the user has no visible repositories", async () => {
  const mock = new MockApiService()
  await mock.register("alice", PW)
  renderWithMock("/alice", mock)
  expect(await screen.findByText("alice has no repositories you can see.")).toBeInTheDocument()
  expect(screen.queryByRole("status", { name: "Loading repositories" })).not.toBeInTheDocument()
})

it("shows the error block and Retry re-requests the same page", async () => {
  routes["GET /api/auth/me"] = loggedOut
  let attempts = 0
  routes["GET /api/repos?owner=alice&page=1"] = () =>
    ++attempts === 1 ? { status: 500, body: { error: "boom" } } : aliceRepos([aRepo()])()
  renderProfile()
  const alert = await screen.findByRole("alert")
  expect(alert).toHaveTextContent("boom")
  await userEvent.click(within(alert).getByRole("button", { name: "Retry" }))
  expect(await screen.findByRole("link", { name: "hello" })).toHaveAttribute("href", "/alice/hello")
  expect(attempts).toBe(2)
  expect(screen.queryByRole("alert")).not.toBeInTheDocument()
})

it.each([
  { who: "alice (the owner)", as: "alice", shown: true },
  { who: "another user", as: "bob", shown: false },
  { who: "a logged-out visitor", as: null, shown: false },
])("Create a repository link for $who: $shown", async ({ as, shown }) => {
  const mock = await withRepos([{ name: "hello" }])
  await mock.register("bob", PW)
  if (as) await mock.login(as, PW)
  else await mock.logout()
  renderWithMock("/alice", mock)
  await screen.findByRole("link", { name: "hello" })
  const link = screen.queryByRole("link", { name: "Create a repository" })
  if (shown) expect(link).toHaveAttribute("href", "/")
  else expect(link).not.toBeInTheDocument()
})

it("badges private repos as Private and public ones as Public", async () => {
  const mock = await withRepos([{ name: "open" }, { name: "hidden", private: true }])
  await mock.login("alice", PW) // the owner sees both
  renderWithMock("/alice", mock)
  const list = within(await screen.findByRole("list", { name: "Repository list" }))
  const row = (name: string) => list.getByRole("link", { name }).closest("li") as HTMLElement
  expect(within(row("open")).getByText("Public")).toBeInTheDocument()
  expect(within(row("hidden")).getByText("Private")).toBeInTheDocument()
})
