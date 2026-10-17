import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import App from "@/App"

type Res = { status: number; body?: unknown }
let routes: Record<string, () => Res>

const json = (status: number, body?: unknown): Res => ({ status, body })
const repo = (empty = false) => ({
  id: 1,
  owner: "alice",
  name: "hello",
  description: "",
  default_branch: "main",
  empty,
  created_at: "2026-01-02T15:04:05Z",
})

function setup(user: string | null, empty = false) {
  routes["GET /api/auth/me"] = () =>
    user ? json(200, { id: 1, username: user }) : json(401, { error: "authentication required" })
  routes["GET /api/repos/alice/hello"] = () => json(200, repo(empty))
  routes["GET /api/repos/alice/hello/branches"] = () =>
    json(200, {
      branches: [
        { name: "main", oid: "a".repeat(40), is_default: true },
        { name: "dev", oid: "b".repeat(40), is_default: false },
      ],
    })
}

beforeEach(() => {
  routes = {}
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const h = routes[`${init?.method ?? "GET"} ${url}`]
      if (!h) throw new Error(`unexpected fetch ${url}`)
      const r = h()
      return { status: r.status, ok: r.status < 300, json: async () => r.body } as Response
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

const renderAt = (path: string) =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  )

it("owner sees General, Commit history, Branches and Danger zone", async () => {
  setup("alice")
  renderAt("/alice/hello/settings")
  expect(await screen.findByRole("heading", { name: "alice/hello settings" })).toBeInTheDocument()
  for (const t of ["General", "Commit history", "Branches", "Danger zone"])
    expect(screen.getByRole("heading", { name: t })).toBeInTheDocument()
  expect(await screen.findByRole("link", { name: "dev" })).toBeInTheDocument()
  expect(screen.getByRole("link", { name: "View history" })).toHaveAttribute("href", "/alice/hello/commits/main")
  expect(screen.getByRole("button", { name: "Rename" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Delete repository" })).toBeInTheDocument()
})

it("a visitor who is not the owner sees a notice and no controls", async () => {
  setup("bob")
  renderAt("/alice/hello/settings")
  expect(await screen.findByText(/Only the owner of alice\/hello/)).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Rename" })).not.toBeInTheDocument()
})

it("an empty repo explains that the history is empty", async () => {
  setup("alice", true)
  renderAt("/alice/hello/settings")
  expect(await screen.findByText(/Nothing has been committed yet/)).toBeInTheDocument()
  expect(screen.queryByRole("heading", { name: "Branches" })).not.toBeInTheDocument()
})

it("the repo page links owners to Settings", async () => {
  setup("alice")
  routes["GET /api/repos/alice/hello/tree/main"] = () => json(200, { ref: "main", commit: "a", path: "", entries: [] })
  routes["GET /api/repos/alice/hello/commits?ref=main&page=1"] = () => json(200, { commits: [], has_more: false })
  renderAt("/alice/hello")
  const link = await screen.findByRole("link", { name: "Settings" })
  expect(link).toHaveAttribute("href", "/alice/hello/settings")
  await userEvent.click(link)
  expect(await screen.findByRole("heading", { name: "General" })).toBeInTheDocument()
})

it("saves a changed description with PATCH and sends only that field", async () => {
  setup("alice")
  let sent: unknown
  routes["PATCH /api/repos/alice/hello"] = () => json(200, { ...repo(), description: "A tiny repo" })
  const fetchMock = vi.mocked(fetch)
  renderAt("/alice/hello/settings")
  const field = await screen.findByLabelText("Description")
  const save = screen.getByRole("button", { name: "Save changes" })
  expect(save).toBeDisabled()
  await userEvent.type(field, "A tiny repo")
  expect(save).toBeEnabled()
  await userEvent.click(save)
  const call = fetchMock.mock.calls.find(([, init]) => init?.method === "PATCH")
  sent = JSON.parse(call![1]!.body as string)
  expect(sent).toEqual({ description: "A tiny repo" })
})

it("blocks saving a description over 500 characters", async () => {
  setup("alice")
  renderAt("/alice/hello/settings")
  const field = await screen.findByLabelText("Description")
  await userEvent.click(field)
  await userEvent.paste("a".repeat(501))
  expect(screen.getByText("501/500 characters")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled()
})
