import { MemoryRouter, renderWithMock } from "@/test-utils"
import { MockApiService } from "@/services/MockApiService"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import App from "@/App"
import { cacheClear } from "@/lib/fetchCache"

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
  cacheClear()
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

/** alice owns "hello" (with a "dev" branch unless empty); `as` is who is logged in. */
async function seeded(as: string | null, empty = false) {
  const mock = new MockApiService()
  await mock.register("alice", "password1")
  await mock.register("bob", "password1")
  await mock.login("alice", "password1")
  await mock.createRepo({ name: "hello" })
  if (!empty) {
    await mock.createCommit("alice", "hello", {
      branch: "main",
      expected_tip: "",
      message: "first",
      changes: [{ path: "README.md", content: "hi\n" }],
    })
    await mock.createBranch("alice", "hello", { name: "dev", from: "main" })
  }
  if (as !== "alice") await mock.logout()
  if (as && as !== "alice") await mock.login(as, "password1")
  return mock
}

const renderAt = (path: string) =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  )

it("owner sees General, Commit history, Branches and Danger zone", async () => {
  renderWithMock("/alice/hello/settings", await seeded("alice"))
  expect(await screen.findByRole("heading", { name: "alice/hello" })).toBeInTheDocument()
  for (const t of ["General", "Commit history", "Branches", "Danger zone"])
    expect(screen.getByRole("heading", { name: t })).toBeInTheDocument()
  expect(await screen.findByRole("link", { name: "dev" })).toBeInTheDocument()
  expect(screen.getByRole("link", { name: "View history" })).toHaveAttribute("href", "/alice/hello/commits/main")
  expect(screen.getByRole("button", { name: "Rename" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Delete repository" })).toBeInTheDocument()
})

it("a visitor who is not the owner sees a notice and no controls", async () => {
  renderWithMock("/alice/hello/settings", await seeded("bob"))
  expect(await screen.findByText(/Only the owner of alice\/hello/)).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Rename" })).not.toBeInTheDocument()
})

it("an empty repo explains that the history is empty", async () => {
  renderWithMock("/alice/hello/settings", await seeded("alice", true))
  expect(await screen.findByText(/Nothing has been committed yet/)).toBeInTheDocument()
  expect(screen.queryByRole("heading", { name: "Branches" })).not.toBeInTheDocument()
})

it("the repo page links owners to Settings", async () => {
  renderWithMock("/alice/hello", await seeded("alice"))
  const link = await screen.findByRole("link", { name: "Settings" })
  expect(link).toHaveAttribute("href", "/alice/hello/settings")
  await userEvent.click(link)
  expect(await screen.findByRole("heading", { name: "General" })).toBeInTheDocument()
})

it("saves a changed description and leaves the other fields alone", async () => {
  const mock = await seeded("alice")
  renderWithMock("/alice/hello/settings", mock)
  const field = await screen.findByLabelText("Description")
  const save = screen.getByRole("button", { name: "Save changes" })
  expect(save).toBeDisabled()
  await userEvent.type(field, "A tiny repo")
  expect(save).toBeEnabled()
  await userEvent.click(save)
  await waitFor(async () => expect((await mock.getRepo("alice", "hello")).description).toBe("A tiny repo"))
  const saved = await mock.getRepo("alice", "hello")
  expect([saved.default_branch, saved.private]).toEqual(["main", false])
})

it("blocks saving a description over 500 characters", async () => {
  renderWithMock("/alice/hello/settings", await seeded("alice"))
  const field = await screen.findByLabelText("Description")
  await userEvent.click(field)
  await userEvent.paste("a".repeat(501))
  expect(screen.getByText("501/500 characters")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled()
})

it("shows Saving… with aria-busy on the visibility confirm while the request is pending", async () => {
  setup("alice")
  let release!: () => void
  const gate = new Promise<void>((r) => (release = r))
  routes["PATCH /api/repos/alice/hello"] = () => json(200, { ...repo(), private: true })
  const inner = vi.mocked(fetch).getMockImplementation()!
  vi.mocked(fetch).mockImplementation(async (u, i) => {
    if (i?.method === "PATCH") await gate
    return inner(u, i)
  })
  renderAt("/alice/hello/settings")
  await userEvent.click(await screen.findByRole("button", { name: "Change visibility" }))
  const confirm = await screen.findByRole("button", { name: "Make private" })
  expect(confirm).not.toHaveAttribute("aria-busy")
  await userEvent.click(confirm)
  const busy = await screen.findByRole("button", { name: "Saving…" })
  expect(busy).toBeDisabled()
  expect(busy).toHaveAttribute("aria-busy", "true")
  release()
  await waitFor(() => expect(screen.queryByRole("button", { name: "Saving…" })).not.toBeInTheDocument())
})

it("section nav wraps instead of clipping on narrow screens (#71)", async () => {
  renderWithMock("/alice/hello/settings", await seeded("alice"))
  const nav = await screen.findByRole("navigation", { name: "Settings sections" })
  expect(nav).toHaveClass("flex-wrap")
  expect(nav).not.toHaveClass("overflow-x-auto")
  expect(Array.from(nav.querySelectorAll("a")).map((a) => a.textContent)).toEqual([
    "General",
    "Commit history",
    "Branches",
    "Danger zone",
  ])
})
