import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, useLocation } from "react-router-dom"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import App from "@/App"

type Res = { status: number; body?: unknown } | Error
let routes: Record<string, (body: unknown) => Res | Promise<Res>>
let calls: string[]

const json = (status: number, body?: unknown): Res => ({ status, body })
const OID = "a".repeat(40)
const OID2 = "b".repeat(40)
const NEW = "c".repeat(40)
const repo = (o: Record<string, unknown> = {}) => ({
  id: 1, owner: "alice", name: "hello", description: "", default_branch: "main",
  empty: false, created_at: "2026-01-02T15:04:05Z", ...o,
})
const blob = (commit = OID, content = "hello\n") => ({
  ref: "main", commit, path: "a.txt", name: "a.txt", oid: OID, size: content.length, content,
})
const branches = {
  branches: [
    { name: "dev", oid: OID2, is_default: false },
    { name: "main", oid: OID, is_default: true },
  ],
}

function setup(user: string | null = "alice", r = repo()) {
  routes["GET /api/auth/me"] = () =>
    user ? json(200, { id: 1, username: user }) : json(401, { error: "authentication required" })
  routes["GET /api/repos/alice/hello"] = () => json(200, r)
  routes["GET /api/repos/alice/hello/branches"] = () => json(200, branches)
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
const commitBtn = () => screen.getByRole("button", { name: "Commit" })
const type = async (label: string, text: string) => {
  const el = screen.getByLabelText(label)
  await userEvent.clear(el)
  if (text) await userEvent.type(el, text)
}

it("edit: prefills, validates the message, commits and redirects", async () => {
  setup()
  routes["GET /api/repos/alice/hello/blob/main/a.txt"] = () => json(200, blob())
  let body: unknown
  routes["POST /api/repos/alice/hello/commits"] = (b) => {
    body = b
    return json(201, { oid: NEW, tree: OID, parent: OID, branch: "main" })
  }
  renderAt("/alice/hello/edit/main/a.txt")
  const area = await screen.findByLabelText("File content")
  expect(area).toHaveValue("hello\n")
  await waitFor(() => expect(commitBtn()).toBeDisabled())
  await type("File content", "bye")
  await userEvent.type(screen.getByLabelText("Commit message"), "   ")
  expect(commitBtn()).toBeDisabled()
  expect(await screen.findByText("Enter a commit message.")).toBeInTheDocument()
  await userEvent.type(screen.getByLabelText("Commit message"), " Update a ")
  await userEvent.click(commitBtn())
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent(`/alice/hello/commit/${NEW}`))
  expect(body).toEqual({
    branch: "main", expected_tip: OID, message: "Update a", changes: [{ path: "a.txt", content: "bye" }],
  })
})

it("new file on an empty repo uses the default branch and an empty expected_tip", async () => {
  setup("alice", repo({ empty: true }))
  let body: unknown
  routes["POST /api/repos/alice/hello/commits"] = (b) => {
    body = b
    return json(201, { oid: NEW, tree: OID, parent: null, branch: "main" })
  }
  renderAt("/alice/hello/new/main")
  await screen.findByLabelText("File path")
  await type("File path", "docs/README.md")
  await type("File content", "")
  await type("Commit message", "First")
  await userEvent.click(commitBtn())
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent(`/commit/${NEW}`))
  expect(body).toEqual({
    branch: "main", expected_tip: "", message: "First", changes: [{ path: "docs/README.md", content: "" }],
  })
  expect(calls.some((c) => c.includes("/tree/"))).toBe(false)
})

it("new file on a non-empty repo takes the tip from the tree", async () => {
  setup()
  routes["GET /api/repos/alice/hello/tree/main"] = () => json(200, { ref: "main", commit: OID, path: "", entries: [] })
  let body: { expected_tip?: string } = {}
  routes["POST /api/repos/alice/hello/commits"] = (b) => {
    body = b as typeof body
    return json(201, { oid: NEW, tree: OID, parent: OID, branch: "main" })
  }
  renderAt("/alice/hello/new/main")
  await screen.findByLabelText("File path")
  await type("File path", "x.txt")
  await type("Commit message", "Add x")
  await waitFor(() => expect(commitBtn()).toBeEnabled())
  await userEvent.click(commitBtn())
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent(`/commit/${NEW}`))
  expect(body.expected_tip).toBe(OID)
})

it("changing the branch reloads that branch's tip", async () => {
  setup()
  routes["GET /api/repos/alice/hello/blob/main/a.txt"] = () => json(200, blob(OID))
  routes["GET /api/repos/alice/hello/blob/dev/a.txt"] = () => json(200, blob(OID2, "other\n"))
  let body: { branch?: string; expected_tip?: string; changes?: unknown } = {}
  routes["POST /api/repos/alice/hello/commits"] = (b) => {
    body = b as typeof body
    return json(201, { oid: NEW, tree: OID, parent: OID2, branch: "dev" })
  }
  renderAt("/alice/hello/edit/main/a.txt")
  await screen.findByLabelText("File content")
  await type("File content", "mine")
  await userEvent.click(await screen.findByRole("combobox", { name: "Branch" }))
  await userEvent.click(await screen.findByRole("option", { name: "dev" }))
  await type("Commit message", "On dev")
  await waitFor(() => expect(commitBtn()).toBeEnabled())
  expect(screen.getByLabelText("File content")).toHaveValue("mine")
  await userEvent.click(commitBtn())
  await waitFor(() => expect(body.branch).toBe("dev"))
  expect(body.expected_tip).toBe(OID2)
})

it("delete asks for confirmation and sends a delete change", async () => {
  setup()
  routes["GET /api/repos/alice/hello/blob/main/a.txt"] = () => json(200, blob())
  let body: { changes?: unknown } = {}
  routes["POST /api/repos/alice/hello/commits"] = (b) => {
    body = b as typeof body
    return json(201, { oid: NEW, tree: OID, parent: OID, branch: "main" })
  }
  renderAt("/alice/hello/edit/main/a.txt")
  await screen.findByLabelText("File content")
  await type("Commit message", "Remove a")
  await userEvent.click(screen.getByRole("button", { name: "Delete file" }))
  expect(calls.some((c) => c.startsWith("POST"))).toBe(false)
  const dialog = await screen.findByRole("alertdialog")
  expect(dialog).toHaveTextContent("Delete a.txt?")
  await userEvent.click(within(dialog).getByRole("button", { name: "Delete file" }))
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent(`/commit/${NEW}`))
  expect(body.changes).toEqual([{ path: "a.txt", delete: true }])
})

it("409 shows the conflict, Reload fetches the new tip, text is kept and resubmit works", async () => {
  setup()
  let tip = OID
  routes["GET /api/repos/alice/hello/blob/main/a.txt"] = () => json(200, blob(tip, tip === OID ? "v1" : "v2"))
  const tips: string[] = []
  routes["POST /api/repos/alice/hello/commits"] = (b) => {
    tips.push((b as { expected_tip: string }).expected_tip)
    return tips.length === 1
      ? json(409, { error: "branch tip has moved", current_tip: OID2 })
      : json(201, { oid: NEW, tree: OID, parent: OID2, branch: "main" })
  }
  renderAt("/alice/hello/edit/main/a.txt")
  await screen.findByLabelText("File content")
  await type("File content", "my text")
  await type("Commit message", "Change")
  await userEvent.click(commitBtn())
  expect(await screen.findByText(/branch tip has moved/i)).toBeInTheDocument()
  tip = OID2
  await userEvent.click(screen.getByRole("button", { name: "Reload" }))
  await waitFor(() => expect(screen.queryByText(/branch tip has moved/i)).not.toBeInTheDocument())
  expect(screen.getByLabelText("File content")).toHaveValue("my text")
  await waitFor(() => expect(commitBtn()).toBeEnabled())
  await userEvent.click(commitBtn())
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent(`/commit/${NEW}`))
  expect(tips).toEqual([OID, OID2])
})

it.each([
  "no changes",
  "invalid path: a//b",
  "file too large: a.txt",
])("400 %s is shown inline and the form keeps its values", async (msg) => {
  setup()
  routes["GET /api/repos/alice/hello/blob/main/a.txt"] = () => json(200, blob())
  routes["POST /api/repos/alice/hello/commits"] = () => json(400, { error: msg })
  renderAt("/alice/hello/edit/main/a.txt")
  await screen.findByLabelText("File content")
  await type("File content", "keep me")
  await type("Commit message", "Msg")
  await waitFor(() => expect(commitBtn()).toBeEnabled())
  await userEvent.click(commitBtn())
  expect(await screen.findByText(msg)).toBeInTheDocument()
  expect(screen.getByLabelText("File content")).toHaveValue("keep me")
  expect(screen.getByLabelText("Commit message")).toHaveValue("Msg")
  expect(commitBtn()).toBeEnabled()
})

it("401 on submit redirects to /login", async () => {
  setup()
  routes["GET /api/repos/alice/hello/blob/main/a.txt"] = () => json(200, blob())
  routes["POST /api/repos/alice/hello/commits"] = () => json(401, { error: "authentication required" })
  renderAt("/alice/hello/edit/main/a.txt")
  await screen.findByLabelText("File content")
  await type("Commit message", "Msg")
  await waitFor(() => expect(commitBtn()).toBeEnabled())
  await userEvent.click(commitBtn())
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/login"))
})

it("403 on submit shows an error", async () => {
  setup()
  routes["GET /api/repos/alice/hello/blob/main/a.txt"] = () => json(200, blob())
  routes["POST /api/repos/alice/hello/commits"] = () => json(403, { error: "forbidden" })
  renderAt("/alice/hello/edit/main/a.txt")
  await screen.findByLabelText("File content")
  await type("Commit message", "Msg")
  await waitFor(() => expect(commitBtn()).toBeEnabled())
  await userEvent.click(commitBtn())
  expect(await screen.findByText("forbidden")).toBeInTheDocument()
})

it("disables Commit while submitting (no double submit)", async () => {
  setup()
  routes["GET /api/repos/alice/hello/blob/main/a.txt"] = () => json(200, blob())
  let release: (r: Res) => void = () => {}
  let posts = 0
  routes["POST /api/repos/alice/hello/commits"] = () => {
    posts++
    return new Promise<Res>((r) => (release = r))
  }
  renderAt("/alice/hello/edit/main/a.txt")
  await screen.findByLabelText("File content")
  await type("Commit message", "Msg")
  await waitFor(() => expect(commitBtn()).toBeEnabled())
  await userEvent.click(commitBtn())
  await waitFor(() => expect(commitBtn()).toBeDisabled())
  expect(posts).toBe(1)
  release(json(201, { oid: NEW, tree: OID, parent: OID, branch: "main" }))
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/commit/"))
})

it("blob 404 shows a not-found state", async () => {
  setup()
  routes["GET /api/repos/alice/hello/blob/main/a.txt"] = () => json(404, { error: "path not found" })
  renderAt("/alice/hello/edit/main/a.txt")
  expect(await screen.findByText("File not found")).toBeInTheDocument()
  expect(screen.queryByLabelText("File content")).not.toBeInTheDocument()
})

it.each([
  ["logged out", null, "/alice/hello/edit/main/a.txt"],
  ["non-owner", "bob", "/alice/hello/new/main"],
  ["commit oid ref", "alice", `/alice/hello/edit/${OID}/a.txt`],
])("%s sees no form", async (_n, user, path) => {
  setup(user)
  renderAt(path)
  expect(await screen.findByTestId("forbidden")).toBeInTheDocument()
  expect(screen.queryByLabelText("File content")).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Commit" })).not.toBeInTheDocument()
})

it("shows a loading state first", async () => {
  setup()
  routes["GET /api/repos/alice/hello/blob/main/a.txt"] = () => json(200, blob())
  renderAt("/alice/hello/edit/main/a.txt")
  expect(screen.getByRole("status", { name: "Loading editor" })).toBeInTheDocument()
  await screen.findByLabelText("File content")
})
