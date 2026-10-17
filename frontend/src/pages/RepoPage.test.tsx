import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, useLocation } from "react-router-dom"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import App from "@/App"
import { nextTask } from "@/test-utils"
import { cacheClear } from "@/lib/fetchCache"
import { formatDate } from "@/lib/format"

type Res = { status: number; body?: unknown } | Error
let routes: Record<string, (body: unknown) => Res | Promise<Res>>
let calls: string[]

const json = (status: number, body?: unknown): Res => ({ status, body })
const me = (username = "alice") => () => json(200, { id: 1, username })
const loggedOut = () => json(401, { error: "authentication required" })
const OID = "a".repeat(40)
const repo = (o: Record<string, unknown> = {}) => ({
  id: 1,
  owner: "alice",
  name: "hello",
  description: "A greeting",
  default_branch: "main",
  empty: false,
  created_at: "2026-01-02T15:04:05Z",
  ...o,
})
const branches = {
  branches: [
    { name: "dev", oid: OID, is_default: false },
    { name: "main", oid: OID, is_default: true },
  ],
}
const tree = (path = "", entries?: unknown[]) => ({
  ref: "main",
  commit: OID,
  path,
  entries: entries ?? [
    { name: "src", type: "tree", oid: OID },
    { name: "README.md", type: "blob", oid: OID },
  ],
})
const commits = {
  commits: [
    { oid: OID, message: "Add readme\n\nlong body", author: "alice", time: "2026-01-02T15:04:05Z", parents: [] },
  ],
}

function setupRepo(user: "alice" | "bob" | null = "alice") {
  routes["GET /api/auth/me"] = user ? me(user) : loggedOut
  routes["GET /api/repos/alice/hello"] = () => json(200, repo())
  routes["GET /api/repos/alice/hello/branches"] = () => json(200, branches)
  routes["GET /api/repos/alice/hello/tree/main"] = () => json(200, tree())
  routes["GET /api/repos/alice/hello/commits?ref=main&page=1"] = () => json(200, commits)
}

beforeEach(() => {
  cacheClear()
  calls = []
  routes = {}
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const key = `${init?.method ?? "GET"} ${url}`
      calls.push(key)
      await nextTask()
      const h = routes[key]
      if (!h) throw new Error(`unexpected fetch ${key}`)
      const r = await h(init?.body ? JSON.parse(init.body as string) : undefined)
      if (r instanceof Error) throw r
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
const main = () => within(screen.getByRole("main"))

it("shows a loading state, then the tree with directories first and links", async () => {
  setupRepo(null)
  renderAt("/alice/hello")
  await screen.findByRole("status", { name: "Loading repository" })
  const files = await screen.findByRole("table", { name: "Files" })
  const links = within(files).getAllByRole("link")
  expect(links.map((l) => l.textContent)).toEqual(["src", "README.md"])
  expect(links[0]).toHaveAttribute("href", "/alice/hello/tree/main/src")
  expect(links[1]).toHaveAttribute("href", "/alice/hello/blob/main/README.md")
})

it("shows the latest commit bar from the first commits item", async () => {
  setupRepo(null)
  renderAt("/alice/hello")
  const oid = await screen.findByRole("link", { name: "aaaaaaa" })
  expect(oid).toHaveAttribute("href", `/alice/hello/commit/${OID}`)
  expect(oid).toHaveAttribute("title", OID)
  expect(main().getByText("Add readme")).toBeInTheDocument()
  expect(main().queryByText(/long body/)).not.toBeInTheDocument()
  expect(main().getByText(formatDate("2026-01-02T15:04:05Z"))).toBeInTheDocument()
})

it("empty repo: no tree or commits calls; visitor sees a plain message", async () => {
  setupRepo(null)
  routes["GET /api/repos/alice/hello"] = () => json(200, repo({ empty: true }))
  renderAt("/alice/hello")
  expect(await screen.findByText(/repository is empty/i)).toBeInTheDocument()
  expect(screen.queryByRole("link", { name: "Add file" })).not.toBeInTheDocument()
  expect(calls.filter((c) => /tree|commits/.test(c))).toEqual([])
})

it("empty repo: owner sees Add file for the default branch", async () => {
  setupRepo()
  routes["GET /api/repos/alice/hello"] = () => json(200, repo({ empty: true }))
  renderAt("/alice/hello")
  expect(await screen.findByRole("link", { name: "Add file" })).toHaveAttribute(
    "href",
    "/alice/hello/new/main",
  )
  expect(calls.filter((c) => /tree|commits/.test(c))).toEqual([])
})

it("unknown repo shows not found; other failures show retry", async () => {
  setupRepo(null)
  routes["GET /api/repos/alice/hello"] = () => json(404, { error: "repository not found" })
  renderAt("/alice/hello")
  expect(await screen.findByText("Repository not found")).toBeInTheDocument()
})

it("500 shows an error with retry that reloads", async () => {
  setupRepo(null)
  routes["GET /api/repos/alice/hello"] = () => json(500, { error: "internal error" })
  renderAt("/alice/hello")
  expect(await screen.findByRole("alert")).toHaveTextContent("internal error")
  routes["GET /api/repos/alice/hello"] = () => json(200, repo())
  await userEvent.click(screen.getByRole("button", { name: "Retry" }))
  expect(await screen.findByRole("table", { name: "Files" })).toBeInTheDocument()
})

it("directory view: breadcrumbs link back; path not found shows not-found state", async () => {
  setupRepo(null)
  routes["GET /api/repos/alice/hello/tree/main/src/lib"] = () =>
    json(200, tree("src/lib", [{ name: "a.go", type: "blob", oid: OID }]))
  renderAt("/alice/hello/tree/main/src/lib")
  const crumbs = await screen.findByRole("navigation", { name: "Breadcrumbs" })
  expect(within(crumbs).getAllByRole("link").map((l) => l.getAttribute("href"))).toEqual([
    "/alice/hello/tree/main",
    "/alice/hello/tree/main/src",
    "/alice/hello/tree/main/src/lib",
  ])
  expect(await screen.findByRole("link", { name: "a.go" })).toHaveAttribute(
    "href",
    "/alice/hello/blob/main/src/lib/a.go",
  )
})

it("path not found and empty directory show a not-found state", async () => {
  setupRepo(null)
  routes["GET /api/repos/alice/hello/tree/main/nope"] = () => json(404, { error: "path not found" })
  routes["GET /api/repos/alice/hello/tree/main/empty"] = () => json(200, tree("empty", []))
  const { unmount } = renderAt("/alice/hello/tree/main/nope")
  expect(await screen.findByTestId("not-found")).toHaveTextContent(/path not found/i)
  unmount()
  renderAt("/alice/hello/tree/main/empty")
  expect(await screen.findByTestId("not-found")).toBeInTheDocument()
})

it("owner on a branch sees Add file, Create branch and Delete repository", async () => {
  setupRepo("alice")
  renderAt("/alice/hello")
  expect(await screen.findByRole("link", { name: "Add file" })).toHaveAttribute(
    "href",
    "/alice/hello/new/main",
  )
  expect(await screen.findByRole("button", { name: "Create branch" })).toBeInTheDocument()
  expect(await screen.findByRole("button", { name: "Delete repository" })).toBeInTheDocument()
})

it.each([["bob"], [null]] as const)("non-owner (%s) sees no write actions", async (u) => {
  setupRepo(u)
  renderAt("/alice/hello")
  await screen.findByRole("table", { name: "Files" })
  for (const n of ["Add file", "Edit", "Create branch", "Delete repository"]) {
    expect(screen.queryByRole("link", { name: n })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: n })).not.toBeInTheDocument()
  }
})

it("matches the owner case-insensitively", async () => {
  setupRepo("alice")
  routes["GET /api/repos/Alice/hello"] = routes["GET /api/repos/alice/hello"]
  routes["GET /api/repos/Alice/hello/branches"] = routes["GET /api/repos/alice/hello/branches"]
  routes["GET /api/repos/Alice/hello/tree/main"] = routes["GET /api/repos/alice/hello/tree/main"]
  routes["GET /api/repos/Alice/hello/commits?ref=main&page=1"] =
    routes["GET /api/repos/alice/hello/commits?ref=main&page=1"]
  renderAt("/Alice/hello")
  expect(await screen.findByRole("button", { name: "Delete repository" })).toBeInTheDocument()
})

it("a commit oid as ref is read-only", async () => {
  setupRepo("alice")
  routes[`GET /api/repos/alice/hello/tree/${OID}`] = () => json(200, tree())
  routes[`GET /api/repos/alice/hello/commits?ref=${OID}&page=1`] = () => json(200, commits)
  renderAt(`/alice/hello/tree/${OID}`)
  await screen.findByRole("table", { name: "Files" })
  expect(screen.queryByRole("link", { name: "Add file" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Create branch" })).not.toBeInTheDocument()
})

it("branch switcher marks the default and switching changes the URL", async () => {
  setupRepo(null)
  routes["GET /api/repos/alice/hello/tree/dev"] = () => json(200, tree())
  routes["GET /api/repos/alice/hello/commits?ref=dev&page=1"] = () => json(200, commits)
  renderAt("/alice/hello")
  await userEvent.click(await screen.findByRole("combobox", { name: "Branch" }))
  expect(screen.getByRole("option", { name: "main (default)" })).toBeInTheDocument()
  await userEvent.click(screen.getByRole("option", { name: "dev" }))
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/tree/dev")
  await screen.findByRole("table", { name: "Files" })
  expect(calls).toContain("GET /api/repos/alice/hello/tree/dev")
})

async function openCreate() {
  await userEvent.click(await screen.findByRole("button", { name: "Create branch" }))
  return screen.getByLabelText("Branch name")
}

it("create branch: posts from the current branch and switches to it", async () => {
  setupRepo("alice")
  routes["POST /api/repos/alice/hello/branches"] = (b) => {
    expect(b).toEqual({ name: "feature", from: "main" })
    return json(201, { name: "feature", oid: OID, is_default: false })
  }
  routes["GET /api/repos/alice/hello/tree/feature"] = () => json(200, tree())
  routes["GET /api/repos/alice/hello/commits?ref=feature&page=1"] = () => json(200, commits)
  renderAt("/alice/hello")
  await userEvent.type(await openCreate(), "feature")
  await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Create branch" }))
  await vi.waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/tree/feature"))
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
})

it.each([
  [400, "invalid branch name"],
  [409, "branch already exists"],
])("create branch %i error is shown inline in the dialog", async (status, msg) => {
  setupRepo("alice")
  routes["POST /api/repos/alice/hello/branches"] = () => json(status, { error: msg })
  renderAt("/alice/hello")
  await userEvent.type(await openCreate(), "x")
  await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Create branch" }))
  expect(await within(screen.getByRole("dialog")).findByRole("alert")).toHaveTextContent(msg)
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello")
})

it("delete repo: dialog names the repo, confirm deletes and redirects with a toast", async () => {
  setupRepo("alice")
  routes["DELETE /api/repos/alice/hello"] = () => json(204)
  routes["GET /api/repos?page=1"] = () => json(200, { repos: [], page: 1, per_page: 50, has_more: false })
  renderAt("/alice/hello")
  await userEvent.click(await screen.findByRole("button", { name: "Delete repository" }))
  const dlg = screen.getByRole("alertdialog")
  expect(dlg).toHaveTextContent("alice/hello")
  await userEvent.click(within(dlg).getByRole("button", { name: "Delete repository" }))
  await vi.waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent(/^\/$/))
  expect(await screen.findByText(/alice\/hello deleted/i)).toBeInTheDocument()
})

it("delete repo: cancel sends no request; failure shows the error", async () => {
  setupRepo("alice")
  renderAt("/alice/hello")
  await userEvent.click(await screen.findByRole("button", { name: "Delete repository" }))
  await userEvent.click(screen.getByRole("button", { name: "Cancel" }))
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument()
  expect(calls.some((c) => c.startsWith("DELETE"))).toBe(false)

  routes["DELETE /api/repos/alice/hello"] = () => json(403, { error: "forbidden" })
  await userEvent.click(screen.getByRole("button", { name: "Delete repository" }))
  await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete repository" }))
  expect(await within(screen.getByRole("alertdialog")).findByRole("alert")).toHaveTextContent("forbidden")
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello")
})

it("delete branch: owner only, default branch excluded", async () => {
  setupRepo("alice")
  renderAt("/alice/hello")
  await userEvent.click(await screen.findByRole("button", { name: "Delete branch" }))
  const dlg = screen.getByRole("dialog")
  expect(within(dlg).getByRole("button", { name: "Delete dev" })).toBeInTheDocument()
  expect(within(dlg).queryByRole("button", { name: "Delete main" })).not.toBeInTheDocument()
})

it.each([["bob"], [null]] as const)("delete branch: hidden for non-owner (%s)", async (u) => {
  setupRepo(u)
  renderAt("/alice/hello")
  await screen.findByRole("table", { name: "Files" })
  expect(screen.queryByRole("button", { name: "Delete branch" })).not.toBeInTheDocument()
})

it("delete branch: confirm calls the API and refreshes the list", async () => {
  setupRepo("alice")
  routes["DELETE /api/repos/alice/hello/branches/dev"] = () => json(204)
  renderAt("/alice/hello")
  await userEvent.click(await screen.findByRole("button", { name: "Delete branch" }))
  await userEvent.click(screen.getByRole("button", { name: "Delete dev" }))
  const dlg = screen.getByRole("alertdialog")
  expect(dlg).toHaveTextContent("dev")
  routes["GET /api/repos/alice/hello/branches"] = () =>
    json(200, { branches: [{ name: "main", oid: OID, is_default: true }] })
  await userEvent.click(within(dlg).getByRole("button", { name: "Delete branch" }))
  expect(await screen.findByText("Branch dev deleted.")).toBeInTheDocument()
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument()
  await vi.waitFor(() => expect(screen.queryByRole("button", { name: "Delete branch" })).not.toBeInTheDocument())
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello")
})

it("delete branch: deleting the current ref moves to the default branch", async () => {
  setupRepo("alice")
  routes["GET /api/repos/alice/hello/tree/dev"] = () => json(200, tree())
  routes["GET /api/repos/alice/hello/commits?ref=dev&page=1"] = () => json(200, commits)
  routes["DELETE /api/repos/alice/hello/branches/dev"] = () => json(204)
  renderAt("/alice/hello/tree/dev")
  await userEvent.click(await screen.findByRole("button", { name: "Delete branch" }))
  await userEvent.click(screen.getByRole("button", { name: "Delete dev" }))
  await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete branch" }))
  await vi.waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/tree/main"))
})

it("delete branch: cancel sends nothing; 400 and 404 errors are shown inline", async () => {
  setupRepo("alice")
  renderAt("/alice/hello")
  await userEvent.click(await screen.findByRole("button", { name: "Delete branch" }))
  await userEvent.click(screen.getByRole("button", { name: "Delete dev" }))
  await userEvent.click(screen.getByRole("button", { name: "Cancel" }))
  expect(calls.some((c) => c.startsWith("DELETE"))).toBe(false)

  for (const [status, msg] of [[400, "cannot delete the default branch"], [404, "branch not found"]] as const) {
    routes["DELETE /api/repos/alice/hello/branches/dev"] = () => json(status, { error: msg })
    await userEvent.click(screen.getByRole("button", { name: "Delete branch" }))
    await userEvent.click(screen.getByRole("button", { name: "Delete dev" }))
    await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete branch" }))
    expect(await within(screen.getByRole("alertdialog")).findByRole("alert")).toHaveTextContent(msg)
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }))
  }
})


const EXPIRED = "Your session has expired. Log in again."
function expectToast() {
  expect(document.querySelector("[data-sonner-toast]")).toHaveTextContent(EXPIRED)
}
function expectLoggedOut() {
  expectToast()
  expect(screen.getByRole("link", { name: "Log in" })).toBeInTheDocument()
}

it("delete branch: 401 expires the session with a toast and hides owner actions", async () => {
  setupRepo("alice")
  routes["DELETE /api/repos/alice/hello/branches/dev"] = () => loggedOut()
  renderAt("/alice/hello")
  await userEvent.click(await screen.findByRole("button", { name: "Delete branch" }))
  await userEvent.click(screen.getByRole("button", { name: "Delete dev" }))
  await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete branch" }))
  await vi.waitFor(() => expectToast())
  expect(calls).toContain("DELETE /api/repos/alice/hello/branches/dev")
  expectLoggedOut()
  expect(screen.queryByRole("button", { name: "Delete branch" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Create branch" })).not.toBeInTheDocument()
})

it("create branch: 401 expires the session with a toast and hides owner actions", async () => {
  setupRepo("alice")
  routes["POST /api/repos/alice/hello/branches"] = () => loggedOut()
  renderAt("/alice/hello")
  await userEvent.type(await openCreate(), "feature")
  await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Create branch" }))
  await vi.waitFor(() => expectToast())
  expect(calls).toContain("POST /api/repos/alice/hello/branches")
  expectLoggedOut()
  expect(screen.queryByRole("button", { name: "Delete branch" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Create branch" })).not.toBeInTheDocument()
})

it("delete repo: 401 expires the session with a toast and stays on the page", async () => {
  setupRepo("alice")
  routes["DELETE /api/repos/alice/hello"] = () => loggedOut()
  renderAt("/alice/hello")
  await userEvent.click(await screen.findByRole("button", { name: "Delete repository" }))
  await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete repository" }))
  await vi.waitFor(() => expectToast())
  expect(calls).toContain("DELETE /api/repos/alice/hello")
  expectLoggedOut()
  expect(screen.queryByRole("button", { name: "Delete repository" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Create branch" })).not.toBeInTheDocument()
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello")
})

// File viewer

const blob = (o: Record<string, unknown> = {}) => ({
  ref: "main",
  commit: OID,
  path: "src/main.go",
  name: "main.go",
  oid: OID,
  size: 28,
  content: "  <b>hi</b>\n\tindent  \n",
  ...o,
})

it("file viewer shows name, size, ref, breadcrumbs and exact content", async () => {
  setupRepo(null)
  routes["GET /api/repos/alice/hello/blob/main/src/main.go"] = () => json(200, blob())
  renderAt("/alice/hello/blob/main/src/main.go")
  expect(await screen.findByRole("heading", { name: "main.go" })).toBeInTheDocument()
  expect(screen.getByTestId("file-content").textContent).toBe("  <b>hi</b>\n\tindent  \n")
  expect(screen.getByText(/28 bytes/)).toBeInTheDocument()
  expect(screen.getByText("main", { selector: "span.font-mono" })).toBeInTheDocument()
  const crumbs = screen.getByRole("navigation", { name: "Breadcrumbs" })
  expect(within(crumbs).getAllByRole("link").map((l) => l.getAttribute("href"))).toEqual([
    "/alice/hello/tree/main",
    "/alice/hello/tree/main/src",
  ])
  expect(screen.queryByRole("link", { name: "Edit" })).not.toBeInTheDocument()
})

it("file viewer: Edit only for the owner on a branch", async () => {
  setupRepo("alice")
  routes["GET /api/repos/alice/hello/blob/main/a.txt"] = () => json(200, blob({ path: "a.txt", name: "a.txt" }))
  routes[`GET /api/repos/alice/hello/blob/${OID}/a.txt`] = () => json(200, blob({ path: "a.txt", name: "a.txt" }))
  const { unmount } = renderAt("/alice/hello/blob/main/a.txt")
  expect(await screen.findByRole("link", { name: "Edit" })).toHaveAttribute("href", "/alice/hello/edit/main/a.txt")
  unmount()
  renderAt(`/alice/hello/blob/${OID}/a.txt`)
  await screen.findByRole("heading", { name: "a.txt" })
  expect(screen.queryByRole("link", { name: "Edit" })).not.toBeInTheDocument()
})

it("file viewer: empty file note, no content block", async () => {
  setupRepo(null)
  routes["GET /api/repos/alice/hello/blob/main/e"] = () => json(200, blob({ name: "e", path: "e", size: 0, content: "" }))
  renderAt("/alice/hello/blob/main/e")
  expect(await screen.findByText(/empty file/i)).toBeInTheDocument()
  expect(screen.queryByTestId("file-content")).not.toBeInTheDocument()
})

it.each([
  [400, "not a file"],
  [404, "path not found"],
  [404, "ref not found"],
])("file viewer %i %s shows a not-found state", async (status, msg) => {
  setupRepo(null)
  routes["GET /api/repos/alice/hello/blob/main/x"] = () => json(status, { error: msg })
  renderAt("/alice/hello/blob/main/x")
  expect(await screen.findByTestId("not-found")).toHaveTextContent(msg)
})

it("file viewer: loading and error with retry", async () => {
  setupRepo(null)
  routes["GET /api/repos/alice/hello/blob/main/x"] = () => json(500, { error: "internal error" })
  renderAt("/alice/hello/blob/main/x")
  expect(screen.getByRole("status", { name: "Loading file" })).toBeInTheDocument()
  expect(await screen.findByRole("alert")).toHaveTextContent("internal error")
  routes["GET /api/repos/alice/hello/blob/main/x"] = () => json(200, blob({ name: "x", path: "x" }))
  await userEvent.click(screen.getByRole("button", { name: "Retry" }))
  expect(await screen.findByRole("heading", { name: "x" })).toBeInTheDocument()
})
