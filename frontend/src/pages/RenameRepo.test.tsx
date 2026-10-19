import { MemoryRouter, renderWithMock } from "@/test-utils"
import { MockApiService } from "@/services/MockApiService"
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useLocation } from "react-router-dom"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import App from "@/App"
import { cacheClear } from "@/lib/fetchCache"

type Res = { status: number; body?: unknown }
let routes: Record<string, (body: unknown) => Res>
let calls: string[]

const json = (status: number, body?: unknown): Res => ({ status, body })
const repo = (name: string, empty = true) => ({
  id: 1,
  owner: "alice",
  name,
  description: "",
  default_branch: "main",
  empty,
  created_at: "2026-01-02T15:04:05Z",
})

function setup(user: string | null = "alice") {
  routes["GET /api/auth/me"] = () => (user ? json(200, { id: 1, username: user }) : json(401, { error: "authentication required" }))
  routes["GET /api/repos/alice/hello"] = () => json(200, repo("hello"))
  routes["GET /api/repos/alice/renamed"] = () => json(200, repo("renamed"))
}

beforeEach(() => {
  cacheClear()
  calls = []
  routes = {}
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const key = `${init?.method ?? "GET"} ${url}`
      calls.push(key + (init?.body ? ` ${init.body}` : ""))
      const h = routes[key]
      if (!h) throw new Error(`unexpected fetch ${key}`)
      const r = h(init?.body ? JSON.parse(init.body as string) : undefined)
      return { status: r.status, ok: r.status < 300, json: async () => r.body } as Response
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

/** A mock where alice owns the empty repository "hello" and is logged in. */
async function seeded() {
  const mock = new MockApiService()
  await mock.register("alice", "password1")
  await mock.login("alice", "password1")
  await mock.createRepo({ name: "hello" })
  return mock
}

function Loc() {
  return <div data-testid="loc">{useLocation().pathname}</div>
}
function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <App />
      <Loc />
    </MemoryRouter>,
  )
}

async function openDialog() {
  await userEvent.click(await screen.findByRole("button", { name: "Rename" }))
  return screen.getByRole("dialog")
}

it("rename: prefilled field, success navigates to the new name with a toast", async () => {
  const mock = await seeded()
  renderWithMock("/alice/hello/settings", mock, <Loc />)
  const dlg = await openDialog()
  const input = within(dlg).getByLabelText("Repository name")
  expect(input).toHaveValue("hello")
  await userEvent.clear(input)
  await userEvent.type(input, "renamed")
  await userEvent.click(within(dlg).getByRole("button", { name: "Rename repository" }))
  await vi.waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/alice/renamed"))
  expect((await mock.getRepo("alice", "renamed")).name).toBe("renamed")
  expect(await screen.findByText(/renamed to alice\/renamed/i)).toBeInTheDocument()
  expect(await screen.findByRole("heading", { name: "alice/renamed" })).toBeInTheDocument()
})

it("rename: a taken name shows the duplicate message inline and stays", async () => {
  const mock = await seeded()
  await mock.createRepo({ name: "hello2" })
  renderWithMock("/alice/hello/settings", mock, <Loc />)
  const dlg = await openDialog()
  await userEvent.type(within(dlg).getByLabelText("Repository name"), "2")
  await userEvent.click(within(dlg).getByRole("button", { name: "Rename repository" }))
  expect(await within(dlg).findByRole("alert")).toHaveTextContent(
    "A repository with this name already exists. Choose another name.",
  )
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/settings")
})

it("rename: 400 shows the name rule; invalid names are rejected before any request", async () => {
  setup()
  routes["PATCH /api/repos/alice/hello"] = () => json(400, { error: "invalid repository name" })
  renderAt("/alice/hello/settings")
  const dlg = await openDialog()
  const input = within(dlg).getByLabelText("Repository name")
  await userEvent.clear(input)
  await userEvent.type(input, "a b")
  await userEvent.click(within(dlg).getByRole("button", { name: "Rename repository" }))
  expect(await within(dlg).findByRole("alert")).toHaveTextContent("letters, digits")
  expect(calls.some((c) => c.startsWith("PATCH"))).toBe(false)

  await userEvent.clear(input)
  await userEvent.type(input, "ok")
  await userEvent.click(within(dlg).getByRole("button", { name: "Rename repository" }))
  expect(await within(dlg).findByRole("alert")).toHaveTextContent("Use 1-100 letters, digits, dots, hyphens or underscores.")
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/settings")
})

it.each([["bob"], [null]])("rename: button hidden for non-owner (%s)", async (u) => {
  const mock = await seeded()
  await mock.register("bob", "password1")
  if (u) await mock.login(u, "password1")
  else await mock.logout()
  renderWithMock("/alice/hello/settings", mock)
  await screen.findByRole("heading", { name: "alice/hello" })
  expect(screen.queryByRole("button", { name: "Rename" })).not.toBeInTheDocument()
})

it("old URL after a rename shows the repository not found state", async () => {
  const mock = await seeded()
  await mock.updateRepo("alice", "hello", { name: "renamed" })
  renderWithMock("/alice/hello/settings", mock)
  expect(await screen.findByText("Repository not found")).toBeInTheDocument()
})

const EXPIRED = "Your session has expired. Log in again."
function expectToast() {
  expect(document.querySelector("[data-sonner-toast]")).toHaveTextContent(EXPIRED)
}
function expectLoggedOut() {
  expectToast()
  expect(within(screen.getByRole("main")).getByRole("link", { name: "Log in" })).toBeInTheDocument()
}

it("rename: 401 expires the session with a toast and hides owner actions", async () => {
  const mock = await seeded()
  renderWithMock("/alice/hello/settings", mock, <Loc />)
  const dlg = await openDialog()
  await mock.logout() // the session ends behind the page's back
  await userEvent.type(within(dlg).getByLabelText("Repository name"), "2")
  await userEvent.click(within(dlg).getByRole("button", { name: "Rename repository" }))
  await vi.waitFor(() => expectToast())
  expectLoggedOut()
  expect(screen.queryByRole("button", { name: "Rename" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Delete repository" })).not.toBeInTheDocument()
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/settings")
})
