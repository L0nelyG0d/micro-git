import { MemoryRouter } from "@/test-utils"
import { renderWithMock } from "@/test-mock"
import { MockApiService } from "@/services/MockApiService"
import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useLocation } from "react-router-dom"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import App from "@/App"
import { cacheClear } from "@/lib/fetchCache"

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
  sessionStorage.clear()
  cacheClear()
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
function renderAt(path: string, history: string[] = []) {
  return render(
    <MemoryRouter
      initialEntries={[...history, path]}
      initialIndex={history.length}
    >
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

it("400 file too large is shown inline and the form keeps its values", async () => {
  setup()
  routes["GET /api/repos/alice/hello/blob/main/a.txt"] = () => json(200, blob())
  routes["POST /api/repos/alice/hello/commits"] = () => json(400, { error: "file too large: a.txt" })
  renderAt("/alice/hello/edit/main/a.txt")
  await screen.findByLabelText("File content")
  await type("File content", "keep me")
  await type("Commit message", "Msg")
  await waitFor(() => expect(commitBtn()).toBeEnabled())
  await userEvent.click(commitBtn())
  expect(await screen.findByText("file too large: a.txt")).toBeInTheDocument()
  expect(screen.getByLabelText("File content")).toHaveValue("keep me")
  expect(screen.getByLabelText("Commit message")).toHaveValue("Msg")
  expect(commitBtn()).toBeEnabled()
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

it("shows a loading state first", async () => {
  setup()
  routes["GET /api/repos/alice/hello/blob/main/a.txt"] = () => json(200, blob())
  renderAt("/alice/hello/edit/main/a.txt")
  expect(screen.getByRole("status", { name: "Loading editor" })).toBeInTheDocument()
  await screen.findByLabelText("File content")
})

const unload = () => {
  const ev = new Event("beforeunload", { cancelable: true })
  window.dispatchEvent(ev)
  return ev
}

const TREE = "/alice/hello/tree/main"
const EDIT = "/alice/hello/edit/main/a.txt"


// ---- Screens on top of MockApiService (#102) ----


/** alice/hello: main has a.txt = "hello\n"; dev has a.txt = "other\n". `as` is who is logged in. */
async function editorMock(as: "alice" | "bob" | null, opts: { empty?: boolean } = {}) {
  const mock = new MockApiService()
  await mock.register("alice", "password1")
  await mock.register("bob", "password1")
  await mock.login("alice", "password1")
  await mock.createRepo({ name: "hello" })
  if (!opts.empty) {
    await mock.createCommit("alice", "hello", {
      branch: "main", expected_tip: "", message: "first", changes: [{ path: "a.txt", content: "hello\n" }],
    })
    await mock.createBranch("alice", "hello", { name: "dev", from: "main" })
    await commitOn(mock, "dev", "other\n")
  }
  await mock.logout()
  if (as) await mock.login(as, "password1")
  return mock
}
const tipOf = async (mock: MockApiService, branch: string) =>
  (await mock.listBranches("alice", "hello")).branches.find((b) => b.name === branch)!.oid
/** A commit by alice on `branch` (e.g. another tab); restores the current session afterwards. */
async function commitOn(mock: MockApiService, branch: string, content: string) {
  const me = await mock.me().catch(() => null)
  if (me?.username !== "alice") await mock.login("alice", "password1")
  await mock.createCommit("alice", "hello", {
    branch, expected_tip: await tipOf(mock, branch), message: `set ${content.trim()}`, changes: [{ path: "a.txt", content }],
  })
  if (me?.username !== "alice") {
    await mock.logout()
    if (me) await mock.login(me.username, "password1")
  }
}
const contentAt = async (mock: MockApiService, ref: string, path = "a.txt") =>
  (await mock.getBlob("alice", "hello", ref, path)).content
const lastCommit = async (mock: MockApiService, ref: string) =>
  (await mock.listCommits("alice", "hello", { ref })).commits[0]

let navRouter: { navigate: (to: number) => Promise<void> } | undefined
const open = (path: string, mock: MockApiService, history: string[] = []) =>
  renderWithMock(path, mock, <Loc />, { history, routerRef: (r) => (navRouter = r) })

it("mock: edit prefills, validates the message, commits and redirects", async () => {
  const mock = await editorMock("alice")
  open(EDIT, mock)
  const area = await screen.findByLabelText("File content")
  expect(area).toHaveValue("hello\n")
  await waitFor(() => expect(commitBtn()).toBeDisabled())
  await type("File content", "bye")
  await userEvent.type(screen.getByLabelText("Commit message"), "   ")
  expect(commitBtn()).toBeDisabled()
  expect(await screen.findByText("Enter a commit message.")).toBeInTheDocument()
  await userEvent.type(screen.getByLabelText("Commit message"), " Update a ")
  await userEvent.click(commitBtn())
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/commit/"))
  expect(await contentAt(mock, "main")).toBe("bye")
  expect((await lastCommit(mock, "main")).message).toBe("Update a")
})

it("mock: new file on an empty repo commits to the default branch", async () => {
  const mock = await editorMock("alice", { empty: true })
  open("/alice/hello/new/main", mock)
  await screen.findByLabelText("File path")
  await type("File path", "docs/README.md")
  await type("File content", "")
  await type("Commit message", "First")
  await userEvent.click(commitBtn())
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/commit/"))
  expect(await contentAt(mock, "main", "docs/README.md")).toBe("")
  expect((await lastCommit(mock, "main")).message).toBe("First")
})

it("mock: new file on a non-empty repo is added on top of the current tip", async () => {
  const mock = await editorMock("alice")
  open("/alice/hello/new/main", mock)
  await screen.findByLabelText("File path")
  await type("File path", "x.txt")
  await type("Commit message", "Add x")
  await waitFor(() => expect(commitBtn()).toBeEnabled())
  await userEvent.click(commitBtn())
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/commit/"))
  expect(await contentAt(mock, "main", "x.txt")).toBe("")
  expect(await contentAt(mock, "main")).toBe("hello\n")
  expect((await mock.listCommits("alice", "hello", { ref: "main" })).commits).toHaveLength(2)
})

it("mock: changing the branch reloads that branch's tip and commits there", async () => {
  const mock = await editorMock("alice")
  open(EDIT, mock)
  await screen.findByLabelText("File content")
  await type("File content", "mine")
  await userEvent.click(await screen.findByRole("combobox", { name: "Branch" }))
  await userEvent.click(await screen.findByRole("option", { name: "dev" }))
  await type("Commit message", "On dev")
  await waitFor(() => expect(commitBtn()).toBeEnabled())
  expect(screen.getByLabelText("File content")).toHaveValue("mine")
  await userEvent.click(commitBtn())
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/commit/"))
  expect(await contentAt(mock, "dev")).toBe("mine")
  expect(await contentAt(mock, "main")).toBe("hello\n")
})

it("mock: delete asks for confirmation and removes the file", async () => {
  const mock = await editorMock("alice")
  open(EDIT, mock)
  await screen.findByLabelText("File content")
  await type("Commit message", "Remove a")
  await userEvent.click(screen.getByRole("button", { name: "Delete file" }))
  const dialog = await screen.findByRole("alertdialog")
  expect(dialog).toHaveTextContent("Delete a.txt?")
  expect((await mock.listCommits("alice", "hello", { ref: "main" })).commits).toHaveLength(1) // nothing sent yet
  await userEvent.click(within(dialog).getByRole("button", { name: "Delete file" }))
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/commit/"))
  await expect(contentAt(mock, "main")).rejects.toMatchObject({ status: 404 })
})

it("mock: 409 shows the conflict, Reload fetches the new tip, text is kept and resubmit works", async () => {
  const mock = await editorMock("alice")
  open(EDIT, mock)
  await screen.findByLabelText("File content")
  await type("File content", "my text")
  await type("Commit message", "Change")
  await commitOn(mock, "main", "theirs\n") // another tab moves main first
  await userEvent.click(commitBtn())
  expect(await screen.findByText(/branch tip has moved/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole("button", { name: "Reload" }))
  await waitFor(() => expect(screen.queryByText(/branch tip has moved/i)).not.toBeInTheDocument())
  expect(screen.getByLabelText("File content")).toHaveValue("my text")
  await waitFor(() => expect(commitBtn()).toBeEnabled())
  await userEvent.click(commitBtn())
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/commit/"))
  expect(await contentAt(mock, "main")).toBe("my text")
  expect((await mock.listCommits("alice", "hello", { ref: "main" })).commits).toHaveLength(3)
})

it("mock: an invalid path is shown inline and the form keeps its values", async () => {
  const mock = await editorMock("alice")
  open("/alice/hello/new/main", mock)
  await screen.findByLabelText("File path")
  await type("File path", "a//b")
  await type("File content", "keep me")
  await type("Commit message", "Msg")
  await waitFor(() => expect(commitBtn()).toBeEnabled())
  await userEvent.click(commitBtn())
  expect(await screen.findByText(/invalid path/)).toBeInTheDocument()
  expect(screen.getByLabelText("File content")).toHaveValue("keep me")
  expect(screen.getByLabelText("Commit message")).toHaveValue("Msg")
  expect(commitBtn()).toBeEnabled()
})

it("mock: an unchanged file is rejected inline and the form keeps its values", async () => {
  const mock = await editorMock("alice")
  open(EDIT, mock)
  await screen.findByLabelText("File content")
  await type("File content", "hello\n")
  await type("Commit message", "Msg")
  await waitFor(() => expect(commitBtn()).toBeEnabled())
  await userEvent.click(commitBtn())
  expect(await screen.findByText("no changes")).toBeInTheDocument()
  expect(screen.getByLabelText("Commit message")).toHaveValue("Msg")
  expect(commitBtn()).toBeEnabled()
})

it("mock: 401 on submit redirects to /login", async () => {
  const mock = await editorMock("alice")
  open(EDIT, mock)
  await screen.findByLabelText("File content")
  await type("Commit message", "Msg")
  await waitFor(() => expect(commitBtn()).toBeEnabled())
  await mock.logout() // the session ends behind the page's back
  await userEvent.click(commitBtn())
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/login"))
})

it("mock: blob 404 shows a not-found state", async () => {
  open("/alice/hello/edit/main/nope.txt", await editorMock("alice"))
  expect(await screen.findByText("File not found")).toBeInTheDocument()
  expect(screen.queryByLabelText("File content")).not.toBeInTheDocument()
})

it.each([
  ["logged out", null, EDIT],
  ["non-owner", "bob", "/alice/hello/new/main"],
  ["commit oid ref", "alice", "OID"],
] as const)("mock: %s sees no form", async (_n, user, path) => {
  const mock = await editorMock(user)
  open(path === "OID" ? `/alice/hello/edit/${await tipOf(mock, "main")}/a.txt` : path, mock)
  expect(await screen.findByTestId("forbidden")).toBeInTheDocument()
  expect(screen.queryByLabelText("File content")).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Commit" })).not.toBeInTheDocument()
})

it("mock: dirty editor: beforeunload is prevented", async () => {
  open(EDIT, await editorMock("alice"))
  await screen.findByLabelText("File content")
  await type("File content", "changed")
  expect(unload().defaultPrevented).toBe(true)
})

it("mock: clean editor: no beforeunload prompt and Cancel leaves silently", async () => {
  open(EDIT, await editorMock("alice"))
  await screen.findByLabelText("File content")
  expect(unload().defaultPrevented).toBe(false)
  await userEvent.click(screen.getByRole("button", { name: "Cancel" }))
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/blob/main/a.txt"))
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument()
})

it("mock: dirty editor: Cancel and breadcrumb links ask first; Keep editing stays, Discard leaves", async () => {
  open(EDIT, await editorMock("alice"))
  await screen.findByLabelText("File content")
  await type("File content", "changed")
  await userEvent.click(screen.getByRole("button", { name: "Cancel" }))
  const dlg = await screen.findByRole("alertdialog")
  expect(screen.getByTestId("loc")).toHaveTextContent("/edit/main/a.txt")
  await userEvent.click(within(dlg).getByRole("button", { name: "Keep editing" }))
  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
  expect(screen.getByLabelText("File content")).toHaveValue("changed")
  await userEvent.click(screen.getByRole("link", { name: "hello" }))
  await userEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Discard changes" }))
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent(/^\/alice\/hello\/tree\/main$/))
  expect(sessionStorage.length).toBe(0)
})

it("mock: successful commit: no prompt and no beforeunload", async () => {
  open(EDIT, await editorMock("alice"))
  await screen.findByLabelText("File content")
  await type("File content", "changed")
  await type("Commit message", "Msg")
  await waitFor(() => expect(commitBtn()).toBeEnabled())
  await userEvent.click(commitBtn())
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/commit/"))
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument()
  expect(unload().defaultPrevented).toBe(false)
  expect(sessionStorage.length).toBe(0)
})

it("mock: 401 keeps the draft and restores it when the editor reopens", async () => {
  const mock = await editorMock("alice")
  const first = open(EDIT, mock)
  await screen.findByLabelText("File content")
  await type("File content", "my draft")
  await type("Commit message", "Msg")
  await waitFor(() => expect(commitBtn()).toBeEnabled())
  await mock.logout()
  await userEvent.click(commitBtn())
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/login"))
  first.unmount()
  await mock.login("alice", "password1")
  open(EDIT, mock)
  expect(await screen.findByLabelText("File content")).toHaveValue("my draft")
  expect(screen.getByLabelText("Commit message")).toHaveValue("Msg")
})

it("mock: closing the leave dialog returns focus to the activated control, else the textarea", async () => {
  open(EDIT, await editorMock("alice"))
  await screen.findByLabelText("File content")
  await type("File content", "changed")
  const cancel = screen.getByRole("button", { name: "Cancel" })
  await userEvent.click(cancel)
  await screen.findByRole("alertdialog")
  await userEvent.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
  await waitFor(() => expect(cancel).toHaveFocus())
  expect(screen.getByLabelText("File content")).toHaveValue("changed")

  const link = screen.getByRole("link", { name: "hello" })
  await userEvent.click(link)
  const dlg = await screen.findByRole("alertdialog")
  await userEvent.click(within(dlg).getByRole("button", { name: "Keep editing" }))
  await waitFor(() => expect(link).toHaveFocus())
})

it("mock: dirty editor: browser Back (history pop) asks first; Keep editing stays, Discard goes back", async () => {
  open(EDIT, await editorMock("alice"), [TREE])
  await screen.findByLabelText("File content")
  await type("File content", "changed")
  void navRouter!.navigate(-1)
  const dlg = await screen.findByRole("alertdialog")
  expect(screen.getByTestId("loc")).toHaveTextContent(EDIT)
  await userEvent.click(within(dlg).getByRole("button", { name: "Keep editing" }))
  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
  expect(screen.getByTestId("loc")).toHaveTextContent(EDIT)
  expect(screen.getByLabelText("File content")).toHaveValue("changed")
  void navRouter!.navigate(-1)
  await userEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Discard changes" }))
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent(/^\/alice\/hello\/tree\/main$/))
})

it("mock: clean editor: browser Back leaves without a prompt", async () => {
  open(EDIT, await editorMock("alice"), [TREE])
  await screen.findByLabelText("File content")
  void navRouter!.navigate(-1)
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent(/^\/alice\/hello\/tree\/main$/))
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument()
})

it("mock: dirty editor: an in-app link asks first, Back after typing then reverting is silent", async () => {
  open(EDIT, await editorMock("alice"), [TREE])
  await screen.findByLabelText("File content")
  await type("File content", "changed")
  await userEvent.click(screen.getByRole("link", { name: "hello" }))
  await userEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Keep editing" }))
  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
  await type("File content", "hello\n")
  expect(unload().defaultPrevented).toBe(false)
  void navRouter!.navigate(-1)
  await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent(/^\/alice\/hello\/tree\/main$/))
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument()
})
