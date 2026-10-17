import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import App from "@/App"
import { nextTask } from "@/test-utils"

type Res = { status: number; body?: unknown } | Error
let routes: Record<string, () => Res>
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
  routes["GET /api/repos/alice/hello/commits?ref=main&page=1"] = () => json(200, { commits: [] })
  routes["GET /api/repos/alice/hello/commits?ref=dev&page=1"] = () => json(200, { commits: [] })
}

beforeEach(() => {
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
      const r = h()
      if (r instanceof Error) throw r
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

it("shows the README rendered below the file tree, markup as text", async () => {
  setup()
  routes["GET /api/repos/alice/hello/blob/main/README.md"] = () =>
    json(200, blob("# Hello\n\nsome **bold** <script>alert(1)</script>\n"))
  renderAt("/alice/hello")
  const card = await screen.findByTestId("readme")
  expect(within(card).getByText("README.md")).toBeInTheDocument()
  expect(await within(card).findByRole("heading", { name: "Hello" })).toBeInTheDocument()
  expect(card).toHaveTextContent("<script>alert(1)</script>")
  expect(card.querySelector("script")).toBeNull()
  expect((await screen.findByRole("table", { name: "Files" })).compareDocumentPosition(card) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
})

it("shows a skeleton while the README loads", async () => {
  setup()
  routes["GET /api/repos/alice/hello/blob/main/README.md"] = () => json(200, blob("x"))
  renderAt("/alice/hello")
  expect(await screen.findByRole("status", { name: "Loading README" })).toBeInTheDocument()
  expect(await screen.findByText("x")).toBeInTheDocument()
})

it("has no card and no fetch when the tree has no README.md", async () => {
  setup(false)
  renderAt("/alice/hello")
  await screen.findByRole("table", { name: "Files" })
  expect(screen.queryByTestId("readme")).not.toBeInTheDocument()
  expect(calls.some((c) => c.includes("/blob/"))).toBe(false)
})

it("has no card in a subdirectory", async () => {
  setup()
  renderAt("/alice/hello/tree/main/src")
  await screen.findByRole("table", { name: "Files" })
  expect(screen.queryByTestId("readme")).not.toBeInTheDocument()
})

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

it("follows the ref: a branch route fetches that branch's README", async () => {
  setup()
  routes["GET /api/repos/alice/hello/blob/dev/README.md"] = () => json(200, blob("dev readme"))
  renderAt("/alice/hello/tree/dev")
  expect(await screen.findByText("dev readme")).toBeInTheDocument()
  expect(calls).toContain("GET /api/repos/alice/hello/blob/dev/README.md")
})
