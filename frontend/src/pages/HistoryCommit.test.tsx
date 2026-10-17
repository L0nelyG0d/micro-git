import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import App from "@/App"
import { nextTask } from "@/test-utils"
import { formatDate } from "@/lib/format"

type Res = { status: number; body?: unknown }
let routes: Record<string, () => Res>
let calls: string[]

const json = (status: number, body?: unknown): Res => ({ status, body })
const A = "a".repeat(40)
const B = "b".repeat(40)
const T = "2026-01-02T15:04:05Z"
const repo = (o: Record<string, unknown> = {}) => ({
  id: 1, owner: "alice", name: "hello", description: "", default_branch: "main",
  empty: false, created_at: T, ...o,
})
const page = (o: Record<string, unknown> = {}) => ({
  ref: "main", page: 1, per_page: 50, has_more: false,
  commits: [{ oid: A, message: "Add readme\n\nlong body", author: "alice", time: T, parents: [B] }],
  ...o,
})
const file = (o: Record<string, unknown>) => ({
  path: "f.txt", status: "modified", additions: 0, deletions: 0, truncated: false, hunks: [], ...o,
})
const detail = (files: unknown[], o: Record<string, unknown> = {}) => ({
  oid: A, tree: B, parents: [B], message: "Add readme\n\nbody text", author: "alice", time: T, files, ...o,
})
const H = "GET /api/repos/alice/hello/commits"

beforeEach(() => {
  calls = []
  routes = { "GET /api/auth/me": () => json(401, { error: "authentication required" }), "GET /api/repos/alice/hello": () => json(200, repo()) }
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    const key = `${init?.method ?? "GET"} ${url}`
    calls.push(key)
    await nextTask()
    const h = routes[key]
    if (!h) throw new Error(`unexpected fetch ${key}`)
    const r = h()
    return { status: r.status, ok: r.status < 300, json: async () => r.body } as Response
  }))
})
afterEach(() => vi.unstubAllGlobals())

const renderAt = (p: string) => render(<MemoryRouter initialEntries={[p]}><App /></MemoryRouter>)
const main = () => within(screen.getByRole("main"))

it("lists commits with first line, author, date and full-oid link", async () => {
  routes[`${H}?ref=main&page=1`] = () => json(200, page())
  renderAt("/alice/hello/commits/main")
  expect(await main().findByRole("status", { name: "Loading history" })).toBeInTheDocument()
  const link = await screen.findByRole("link", { name: "aaaaaaa" })
  expect(link).toHaveAttribute("href", `/alice/hello/commit/${A}`)
  expect(main().getByText("Add readme")).toBeInTheDocument()
  expect(main().queryByText(/long body/)).not.toBeInTheDocument()
  expect(main().getByText(formatDate(T))).toBeInTheDocument()
})

it("paginates with page in the URL: Previous disabled on page 1, Next follows has_more", async () => {
  routes[`${H}?ref=main&page=1`] = () => json(200, page({ has_more: true }))
  routes[`${H}?ref=main&page=2`] = () => json(200, page({ page: 2, has_more: false }))
  renderAt("/alice/hello/commits/main")
  await screen.findByRole("link", { name: "aaaaaaa" })
  expect(screen.getByRole("button", { name: "Previous" })).toBeDisabled()
  await userEvent.click(screen.getByRole("link", { name: "Next" }))
  await vi.waitFor(() => expect(calls).toContain(`${H}?ref=main&page=2`))
  expect(await screen.findByText("Page 2")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Next" })).toBeDisabled()
  expect(screen.getByRole("link", { name: "Previous" })).toHaveAttribute("href", "/alice/hello/commits/main")
})

it("a page past the end shows an empty state", async () => {
  routes[`${H}?ref=main&page=9`] = () => json(200, page({ page: 9, commits: [] }))
  renderAt("/alice/hello/commits/main?page=9")
  expect(await screen.findByText(/no more commits/i)).toBeInTheDocument()
  expect(screen.getByRole("link", { name: "Go to the first page" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Next" })).toBeDisabled()
})

it("unknown ref is not found; 500 shows retry", async () => {
  routes[`${H}?ref=nope&page=1`] = () => json(404, { error: "ref not found" })
  renderAt("/alice/hello/commits/nope")
  expect(await screen.findByText("History not found")).toBeInTheDocument()
})

it("500 shows an error that retries", async () => {
  routes[`${H}?ref=main&page=1`] = () => json(500, { error: "internal error" })
  renderAt("/alice/hello/commits/main")
  expect(await screen.findByRole("alert")).toHaveTextContent("internal error")
  routes[`${H}?ref=main&page=1`] = () => json(200, page())
  await userEvent.click(screen.getByRole("button", { name: "Retry" }))
  expect(await screen.findByRole("link", { name: "aaaaaaa" })).toBeInTheDocument()
})

it("empty repo shows the message without calling commits", async () => {
  routes["GET /api/repos/alice/hello"] = () => json(200, repo({ empty: true }))
  renderAt("/alice/hello/commits/main")
  expect(await screen.findByText(/repository is empty/i)).toBeInTheDocument()
  expect(calls.filter((c) => c.includes("/commits"))).toEqual([])
})

it("a commit oid works as ref", async () => {
  routes[`${H}?ref=${A}&page=1`] = () => json(200, page({ ref: A }))
  renderAt(`/alice/hello/commits/${A}`)
  expect(await screen.findByRole("link", { name: "aaaaaaa" })).toBeInTheDocument()
  expect(main().getByText(/read-only/)).toBeInTheDocument()
})

const D = "GET /api/repos/alice/hello/commits/" + A

it("commit detail: header, parent link, summary, modified file with hunk and markers", async () => {
  routes[D] = () =>
    json(200, detail([
      file({
        path: "src/a.txt", additions: 1, deletions: 1,
        hunks: [{ old_start: 3, old_lines: 2, new_start: 3, new_lines: 2, lines: [
          { type: "context", text: "  keep" },
          { type: "del", text: "old <script>x</script>" },
          { type: "add", text: "new" },
        ] }],
      }),
      file({ path: "z.txt", status: "added", additions: 1, hunks: [{ old_start: 0, old_lines: 0, new_start: 1, new_lines: 1, lines: [{ type: "add", text: "hi" }] }] }),
      file({ path: "gone.txt", status: "deleted", deletions: 1, hunks: [{ old_start: 1, old_lines: 1, new_start: 0, new_lines: 0, lines: [{ type: "del", text: "bye" }] }] }),
    ]))
  renderAt(`/alice/hello/commit/${A}`)
  expect(await screen.findByRole("heading", { level: 1 })).toHaveTextContent("Add readme")
  expect(main().getByText(/body text/)).toBeInTheDocument()
  expect(screen.getByRole("link", { name: "bbbbbbb" })).toHaveAttribute("href", `/alice/hello/commit/${B}`)
  expect(main().getByTestId("summary")).toHaveTextContent("3 files changed, 2 additions, 2 deletions")

  const mod = within(screen.getByRole("region", { name: "src/a.txt" }))
  expect(mod.getByTestId("file-status")).toHaveTextContent("modified")
  expect(mod.getByTestId("hunk-header")).toHaveTextContent("@@ -3,2 +3,2 @@")
  expect(mod.getByTestId("line-del")).toHaveTextContent("-old <script>x</script>")
  expect(mod.getByTestId("line-add")).toHaveTextContent("+new")
  expect(mod.getByTestId("line-context").textContent).toContain("  keep")
  expect(document.querySelector("script")).toBeNull()

  const added = within(screen.getByRole("region", { name: "z.txt" }))
  expect(added.getByTestId("file-status")).toHaveTextContent("added")
  expect(added.getByTestId("hunk-header")).toHaveTextContent("@@ +1,1 @@")
  const del = within(screen.getByRole("region", { name: "gone.txt" }))
  expect(del.getByTestId("file-status")).toHaveTextContent("deleted")
  expect(del.getByTestId("hunk-header")).toHaveTextContent("@@ -1,1 @@")
})

it("root commit, truncated file and zero-hunk file", async () => {
  routes[D] = () =>
    json(200, detail([
      file({ path: "big.txt", status: "added", truncated: true }),
      file({ path: "nl.txt" }),
    ], { parents: [] }))
  renderAt(`/alice/hello/commit/${A}`)
  expect(await screen.findByText("root commit")).toBeInTheDocument()
  expect(main().getByText("diff too large to display")).toBeInTheDocument()
  expect(main().getByText("no visible changes (only the final newline differs)")).toBeInTheDocument()
})

it("commit with no files", async () => {
  routes[D] = () => json(200, detail([]))
  renderAt(`/alice/hello/commit/${A}`)
  expect(await screen.findByText("no files changed")).toBeInTheDocument()
  expect(main().getByTestId("summary")).toHaveTextContent("0 files changed, 0 additions, 0 deletions")
})

it.each([
  [400, "invalid commit id"],
  [404, "commit not found"],
])("%i shows not found", async (status, error) => {
  routes[D] = () => json(status, { error })
  renderAt(`/alice/hello/commit/${A}`)
  expect(await screen.findByText("Commit not found")).toBeInTheDocument()
})

it("500 on commit detail shows retry", async () => {
  routes[D] = () => json(500, { error: "internal error" })
  renderAt(`/alice/hello/commit/${A}`)
  expect(await screen.findByRole("alert")).toHaveTextContent("internal error")
  expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument()
})
