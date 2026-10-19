import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import App from "@/App"
import { MemoryRouter, nextTask } from "@/test-utils"
import { renderWithMock } from "@/test-mock"
import { MockApiService } from "@/services/MockApiService"
import { cacheClear } from "@/lib/fetchCache"

type Res = { status: number; body?: unknown } | Error
let routes: Record<string, () => Res | Promise<Res>>
let calls: string[]

const json = (status: number, body?: unknown): Res => ({ status, body })
const OID = "a".repeat(40)
const entries = (withReadme: boolean) => [
  { name: "src", type: "tree", oid: OID },
  ...(withReadme ? [{ name: "README.md", type: "blob", oid: OID }] : []),
]
const tree = (path: string, e: unknown[]) => ({ ref: "main", commit: OID, path, entries: e })
const blob = (content: string) => ({
  ref: "main",
  commit: OID,
  path: "README.md",
  name: "README.md",
  oid: OID,
  size: content.length,
  content,
})

function setup(withReadme = true) {
  routes["GET /api/auth/me"] = () => json(401, { error: "authentication required" })
  routes["GET /api/repos/alice/hello"] = () =>
    json(200, { id: 1, owner: "alice", name: "hello", description: "", default_branch: "main", empty: false, created_at: "2026-01-02T15:04:05Z" })
  routes["GET /api/repos/alice/hello/branches"] = () =>
    json(200, {
      branches: [
        { name: "dev", oid: OID, is_default: false },
        { name: "main", oid: OID, is_default: true },
      ],
    })
  routes["GET /api/repos/alice/hello/tree/main"] = () => json(200, tree("", entries(withReadme)))
  routes["GET /api/repos/alice/hello/tree/dev"] = () => json(200, tree("", entries(withReadme)))
  routes["GET /api/repos/alice/hello/tree/main/src"] = () =>
    json(200, tree("src", [{ name: "README.md", type: "blob", oid: OID }]))
  routes["GET /api/repos/alice/hello/commits?page=1"] = () => json(200, { commits: [] })
  routes["GET /api/repos/alice/hello/commits?ref=main&page=1"] = () => json(200, { commits: [] })
  routes["GET /api/repos/alice/hello/commits?ref=dev&page=1"] = () => json(200, { commits: [] })
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
      const r = await h()
      if (r instanceof Error) throw r
      return { status: r.status, ok: r.status < 300, json: async () => r.body } as Response
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

/** Logged-out visitor on a mock where alice has a repo "hello" with the given files on main. */
async function withFiles(files: Record<string, string>, devReadme?: string) {
  const mock = new MockApiService()
  await mock.register("alice", "password1")
  await mock.login("alice", "password1")
  await mock.createRepo({ name: "hello" })
  await mock.createCommit("alice", "hello", {
    branch: "main",
    expected_tip: "",
    message: "files",
    changes: Object.entries(files).map(([path, content]) => ({ path, content })),
  })
  if (devReadme !== undefined) {
    await mock.createBranch("alice", "hello", { name: "dev", from: "main" })
    const tip = (await mock.listBranches("alice", "hello")).branches.find((b) => b.name === "dev")!.oid
    await mock.createCommit("alice", "hello", {
      branch: "dev",
      expected_tip: tip,
      message: "dev readme",
      changes: [{ path: "README.md", content: devReadme }],
    })
  }
  await mock.logout()
  return mock
}

const renderAt = (path: string) =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  )

it("shows the README rendered below the file tree, markup as text", async () => {
  const mock = await withFiles({
    "README.md": "# Hello\n\nsome **bold** <script>alert(1)</script>\n",
    "src/a.txt": "a",
  })
  renderWithMock("/alice/hello", mock)
  const card = await screen.findByTestId("readme")
  expect(within(card).getByText("README.md")).toBeInTheDocument()
  expect(await within(card).findByRole("heading", { name: "Hello" })).toBeInTheDocument()
  expect(card).toHaveTextContent("<script>alert(1)</script>")
  expect(card.querySelector("script")).toBeNull()
  expect((await screen.findByRole("table", { name: "Files" })).compareDocumentPosition(card) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
})

// Kept on fetch: needs a request held pending.
it("shows a skeleton while the README loads", async () => {
  setup()
  let release: () => void = () => {}
  const gate = new Promise<void>((r) => (release = r))
  routes["GET /api/repos/alice/hello/blob/main/README.md"] = async () => {
    await gate
    return json(200, blob("x"))
  }
  renderAt("/alice/hello")
  expect(await screen.findByRole("status", { name: "Loading README" })).toBeInTheDocument()
  release()
  expect(await screen.findByText("x")).toBeInTheDocument()
})

it("has no card when the tree has no README.md", async () => {
  renderWithMock("/alice/hello", await withFiles({ "src/a.txt": "a" }))
  await screen.findByRole("table", { name: "Files" })
  expect(screen.queryByTestId("readme")).not.toBeInTheDocument()
})

it("has no card in a subdirectory", async () => {
  renderWithMock("/alice/hello/tree/main/src", await withFiles({ "README.md": "# Top", "src/README.md": "# Inner" }))
  await screen.findByRole("table", { name: "Files" })
  expect(screen.queryByTestId("readme")).not.toBeInTheDocument()
})

// Kept on fetch: mock has no 500.
it("failed README fetch shows a note with retry and keeps the tree", async () => {
  setup()
  let fail = true
  routes["GET /api/repos/alice/hello/blob/main/README.md"] = () =>
    fail ? json(500, { error: "internal error" }) : json(200, blob("back"))
  renderAt("/alice/hello")
  const card = await screen.findByTestId("readme")
  expect(await within(card).findByRole("alert")).toHaveTextContent("Could not load the README")
  expect(await screen.findByRole("table", { name: "Files" })).toBeInTheDocument()
  fail = false
  await userEvent.click(within(card).getByRole("button", { name: "Retry" }))
  expect(await within(card).findByText("back")).toBeInTheDocument()
})

it("follows the ref: a branch route shows that branch's README", async () => {
  renderWithMock("/alice/hello/tree/dev", await withFiles({ "README.md": "main readme" }, "dev readme"))
  expect(await screen.findByText("dev readme")).toBeInTheDocument()
  expect(screen.queryByText("main readme")).not.toBeInTheDocument()
})

it("does not request the README when the tree lists none", async () => {
  const mock = await withFiles({ "src/a.txt": "a" })
  const blob = vi.spyOn(mock, "getBlob")
  renderWithMock("/alice/hello", mock)
  expect(await screen.findByRole("table", { name: "Files" })).toBeInTheDocument()
  expect(screen.queryByTestId("readme")).not.toBeInTheDocument()
  expect(blob).not.toHaveBeenCalled()
})
