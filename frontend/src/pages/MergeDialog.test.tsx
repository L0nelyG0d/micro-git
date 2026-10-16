import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, useLocation } from "react-router-dom"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import App from "@/App"

type Res = { status: number; body?: unknown }
let routes: Record<string, (body: unknown) => Res>
let calls: string[]

const json = (status: number, body?: unknown): Res => ({ status, body })
const OID = "a".repeat(40)
const OID2 = "b".repeat(40)
const branches = {
  branches: [
    { name: "dev", oid: OID2, is_default: false },
    { name: "main", oid: OID, is_default: true },
  ],
}

function setup(user: string | null = "alice", list = branches) {
  routes["GET /api/auth/me"] = () =>
    user ? json(200, { id: 1, username: user }) : json(401, { error: "authentication required" })
  routes["GET /api/repos/alice/hello"] = () =>
    json(200, {
      id: 1, owner: "alice", name: "hello", description: "", default_branch: "main",
      empty: false, created_at: "2026-01-02T15:04:05Z",
    })
  routes["GET /api/repos/alice/hello/branches"] = () => json(200, list)
  routes["GET /api/repos/alice/hello/tree/main"] = () =>
    json(200, { ref: "main", commit: OID, path: "", entries: [{ name: "a.txt", type: "blob", oid: OID }] })
  routes["GET /api/repos/alice/hello/tree/dev"] = routes["GET /api/repos/alice/hello/tree/main"]
  const commits = () =>
    json(200, { commits: [{ oid: OID, message: "m", author: "alice", time: "2026-01-02T15:04:05Z", parents: [] }] })
  routes["GET /api/repos/alice/hello/commits?ref=main&page=1"] = commits
  routes["GET /api/repos/alice/hello/commits?ref=dev&page=1"] = commits
}

beforeEach(() => {
  calls = []
  routes = {}
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const key = `${init?.method ?? "GET"} ${url}`
      calls.push(key)
      const h = routes[key]
      if (!h) throw new Error(`unexpected fetch ${key}`)
      const r = h(init?.body ? JSON.parse(init.body as string) : undefined)
      return { status: r.status, ok: r.status < 300, json: async () => r.body } as Response
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

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
  await userEvent.click(await screen.findByRole("button", { name: "Merge" }))
  return screen.getByRole("alertdialog")
}

it("owner with two branches sees Merge; defaults into=main, from=dev", async () => {
  setup()
  renderAt("/alice/hello")
  const dlg = await openDialog()
  expect(within(dlg).getByRole("combobox", { name: "Into" })).toHaveTextContent("main")
  expect(within(dlg).getByRole("combobox", { name: "Merge from" })).toHaveTextContent("dev")
  expect(within(dlg).getByText("Fast-forward only. main will point to the tip of dev.")).toBeInTheDocument()
  expect(within(dlg).getByRole("button", { name: "Merge" })).toBeEnabled()
})

it("owner repo page shows Compare, Merge, Rename and Delete together", async () => {
  setup()
  renderAt("/alice/hello")
  expect(await screen.findByRole("link", { name: "Compare" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Merge" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Rename" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Delete repository" })).toBeInTheDocument()
})

it("is hidden from non-owners and on a single-branch repo", async () => {
  setup("bob")
  const { unmount } = renderAt("/alice/hello")
  await screen.findByRole("table", { name: "Files" })
  expect(screen.queryByRole("button", { name: "Merge" })).not.toBeInTheDocument()
  unmount()
  setup("alice", { branches: [branches.branches[1]] })
  renderAt("/alice/hello")
  await screen.findByRole("table", { name: "Files" })
  expect(screen.queryByRole("button", { name: "Merge" })).not.toBeInTheDocument()
})

it("submit is disabled while both selects are the same branch", async () => {
  setup()
  renderAt("/alice/hello")
  const dlg = await openDialog()
  await userEvent.click(within(dlg).getByRole("combobox", { name: "Merge from" }))
  await userEvent.click(screen.getByRole("option", { name: "main (default)" }))
  expect(within(dlg).getByRole("button", { name: "Merge" })).toBeDisabled()
})

it("success: posts base/head, toasts, closes and reloads the target branch", async () => {
  setup()
  routes["POST /api/repos/alice/hello/merge"] = (b) => {
    expect(b).toEqual({ base: "main", head: "dev" })
    return json(200, { branch: "main", oid: OID2, previous_oid: OID })
  }
  renderAt("/alice/hello")
  const dlg = await openDialog()
  await userEvent.click(within(dlg).getByRole("button", { name: "Merge" }))
  await vi.waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
  expect(await screen.findByText("main now points to the tip of dev.")).toBeInTheDocument()
  await vi.waitFor(() =>
    expect(calls.filter((c) => c === "GET /api/repos/alice/hello/tree/main").length).toBe(2),
  )
  expect(calls.filter((c) => c === "GET /api/repos/alice/hello/commits?ref=main&page=1").length).toBe(2)
})

it("success with a different target navigates to it", async () => {
  setup()
  routes["POST /api/repos/alice/hello/merge"] = (b) => {
    expect(b).toEqual({ base: "dev", head: "main" })
    return json(200, { branch: "dev", oid: OID, previous_oid: OID2 })
  }
  renderAt("/alice/hello")
  const dlg = await openDialog()
  await userEvent.click(within(dlg).getByRole("combobox", { name: "Into" }))
  await userEvent.click(screen.getByRole("option", { name: "dev" }))
  await userEvent.click(within(dlg).getByRole("combobox", { name: "Merge from" }))
  await userEvent.click(screen.getByRole("option", { name: "main (default)" }))
  await userEvent.click(within(dlg).getByRole("button", { name: "Merge" }))
  await vi.waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/tree/dev"))
})

it("409 not a fast-forward shows the message and advice, dialog stays open", async () => {
  setup()
  routes["POST /api/repos/alice/hello/merge"] = () => json(409, { error: "not a fast-forward" })
  renderAt("/alice/hello")
  const dlg = await openDialog()
  await userEvent.click(within(dlg).getByRole("button", { name: "Merge" }))
  const alert = await within(dlg).findByRole("alert")
  expect(alert).toHaveTextContent("not a fast-forward")
  expect(alert).toHaveTextContent(/create a new branch or commit on top of the target/i)
  expect(within(dlg).getByRole("button", { name: "Merge" })).toBeEnabled()
})

it("409 branch tip has moved offers Reload, which reloads the page data", async () => {
  setup()
  routes["POST /api/repos/alice/hello/merge"] = () =>
    json(409, { error: "branch tip has moved", current_tip: OID2 })
  renderAt("/alice/hello")
  const dlg = await openDialog()
  await userEvent.click(within(dlg).getByRole("button", { name: "Merge" }))
  const alert = await within(dlg).findByRole("alert")
  expect(alert).toHaveTextContent("branch tip has moved")
  await userEvent.click(within(alert).getByRole("button", { name: "Reload" }))
  await vi.waitFor(() =>
    expect(calls.filter((c) => c === "GET /api/repos/alice/hello/branches").length).toBe(2),
  )
})

it("400 already up to date is shown inline", async () => {
  setup()
  routes["POST /api/repos/alice/hello/merge"] = () => json(400, { error: "already up to date" })
  renderAt("/alice/hello")
  const dlg = await openDialog()
  await userEvent.click(within(dlg).getByRole("button", { name: "Merge" }))
  expect(await within(dlg).findByRole("alert")).toHaveTextContent("already up to date")
})
