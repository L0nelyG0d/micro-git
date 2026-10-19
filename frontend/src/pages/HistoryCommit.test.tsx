import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import App from "@/App"
import { MemoryRouter, nextTask } from "@/test-utils"
import { renderWithMock } from "@/test-mock"
import { MockApiService } from "@/services/MockApiService"
import type { Change } from "@/lib/git"
import { cacheClear } from "@/lib/fetchCache"
import { formatDate } from "@/lib/format"

type Res = { status: number; body?: unknown }
let routes: Record<string, () => Res | Promise<Res>>
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
  cacheClear()
  calls = []
  routes = { "GET /api/auth/me": () => json(401, { error: "authentication required" }), "GET /api/repos/alice/hello": () => json(200, repo()) }
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    const key = `${init?.method ?? "GET"} ${url}`
    calls.push(key)
    await nextTask()
    const h = routes[key]
    if (!h) throw new Error(`unexpected fetch ${key}`)
    const r = await h()
    return { status: r.status, ok: r.status < 300, json: async () => r.body } as Response
  }))
})
afterEach(() => vi.unstubAllGlobals())

const renderAt = (p: string) => render(<MemoryRouter initialEntries={[p]}><App /></MemoryRouter>)
/** Logged-out visitor on a mock where alice/hello got these commits in order; returns the oids. */
async function withCommits(...commits: { message: string; changes: Change[] }[]) {
  const mock = new MockApiService()
  await mock.register("alice", "password1")
  await mock.login("alice", "password1")
  await mock.createRepo({ name: "hello" })
  const oids: string[] = []
  let tip = ""
  for (const c of commits) {
    tip = (await mock.createCommit("alice", "hello", { branch: "main", expected_tip: tip, ...c })).oid
    oids.push(tip)
  }
  await mock.logout()
  return { mock, oids }
}
const put = (path: string, content: string): Change => ({ path, content })
const main = () => within(screen.getByRole("main"))

// Kept on fetch: the mock answers at once, so a held request is the only way to observe the loading state.
it("shows a loading state while the history request is pending", async () => {
  routes[`${H}?ref=main&page=1`] = () => new Promise<Res>(() => {})
  renderAt("/alice/hello/commits/main")
  expect(await main().findByRole("status", { name: "Loading history" })).toBeInTheDocument()
})

it("a page past the end shows an empty state", async () => {
  const { mock } = await withCommits({ message: "first", changes: [put("a.txt", "a")] })
  renderWithMock("/alice/hello/commits/main?page=9", mock)
  expect(await screen.findByText(/no more commits/i)).toBeInTheDocument()
  expect(screen.getByRole("link", { name: "Go to the first page" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Next" })).toBeDisabled()
})

it("unknown ref is not found", async () => {
  const { mock } = await withCommits({ message: "first", changes: [put("a.txt", "a")] })
  renderWithMock("/alice/hello/commits/nope", mock)
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

it("empty repo shows the message", async () => {
  const { mock } = await withCommits()
  renderWithMock("/alice/hello/commits/main", mock)
  expect(await screen.findByText(/repository is empty/i)).toBeInTheDocument()
})

it("a commit oid works as ref", async () => {
  const { mock, oids } = await withCommits({ message: "first", changes: [put("a.txt", "a")] })
  renderWithMock(`/alice/hello/commits/${oids[0]}`, mock)
  expect(await screen.findByRole("link", { name: oids[0].slice(0, 7) })).toBeInTheDocument()
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
  expect(screen.getByRole("link", { name: "alice/hello" })).toHaveClass("max-sm:break-all")

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

it("root commit shows as such, and a later commit links to its parent with added, modified and deleted files", async () => {
  const { mock, oids } = await withCommits(
    { message: "first", changes: [put("src/a.txt", "keep\nold <script>x</script>\n"), put("gone.txt", "bye\n")] },
    {
      message: "second\n\nbody text",
      changes: [put("src/a.txt", "keep\nnew\n"), put("z.txt", "hi\n"), { path: "gone.txt", delete: true }],
    },
  )
  const view = renderWithMock(`/alice/hello/commit/${oids[0]}`, mock)
  expect(await screen.findByText("root commit")).toBeInTheDocument()
  view.unmount()

  renderWithMock(`/alice/hello/commit/${oids[1]}`, mock)
  expect(await screen.findByRole("heading", { level: 1 })).toHaveTextContent("second")
  expect(main().getByText(/body text/)).toBeInTheDocument()
  expect(screen.getByRole("link", { name: oids[0].slice(0, 7) })).toHaveAttribute("href", `/alice/hello/commit/${oids[0]}`)
  expect(main().getByTestId("summary")).toHaveTextContent("3 files changed")
  const status = (path: string) => within(screen.getByRole("region", { name: path })).getByTestId("file-status")
  expect(status("src/a.txt")).toHaveTextContent("modified")
  expect(status("z.txt")).toHaveTextContent("added")
  expect(status("gone.txt")).toHaveTextContent("deleted")
  const mod = within(screen.getByRole("region", { name: "src/a.txt" }))
  expect(mod.getByTestId("line-add")).toHaveTextContent("+new")
  expect(mod.getByTestId("line-del")).toHaveTextContent("-old <script>x</script>")
  expect(document.querySelector("script")).toBeNull()
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

it.each([
  ["an invalid commit id", "nope"],
  ["an unknown commit", "c".repeat(40)],
])("%s shows not found", async (_what, oid) => {
  const { mock } = await withCommits({ message: "first", changes: [put("a.txt", "a")] })
  renderWithMock(`/alice/hello/commit/${oid}`, mock)
  expect(await screen.findByText("Commit not found")).toBeInTheDocument()
})

it("500 on commit detail shows retry", async () => {
  routes[D] = () => json(500, { error: "internal error" })
  renderAt(`/alice/hello/commit/${A}`)
  expect(await screen.findByRole("alert")).toHaveTextContent("internal error")
  expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument()
})

it("lists commits with first line, author, date and full-oid link", async () => {
  const { mock, oids } = await withCommits({ message: "Add readme\n\nlong body", changes: [put("README.md", "# hi\n")] })
  const when = (await mock.listCommits("alice", "hello", { ref: "main" })).commits[0].time
  renderWithMock("/alice/hello/commits/main", mock)
  const link = await screen.findByRole("link", { name: oids[0].slice(0, 7) })
  expect(link).toHaveAttribute("href", `/alice/hello/commit/${oids[0]}`)
  expect(main().getByText("main", { selector: "span.font-mono" })).toHaveClass("max-sm:break-all")
  expect(main().getByText("Add readme")).toBeInTheDocument()
  expect(main().queryByText(/long body/)).not.toBeInTheDocument()
  expect(main().getByText("alice")).toBeInTheDocument()
  expect(main().getByText(formatDate(when))).toBeInTheDocument()
})

it("paginates with page in the URL: Previous disabled on page 1, Next follows has_more", async () => {
  const { mock } = await withCommits(
    ...Array.from({ length: 51 }, (_, i) => ({ message: `c${i}`, changes: [put("a.txt", String(i))] })),
  )
  renderWithMock("/alice/hello/commits/main", mock)
  await screen.findByText("c50")
  expect(screen.getByRole("button", { name: "Previous" })).toBeDisabled()
  await userEvent.click(screen.getByRole("link", { name: "Next" }))
  expect(await screen.findByText("Page 2")).toBeInTheDocument()
  expect(await screen.findByText("c0")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Next" })).toBeDisabled()
  expect(screen.getByRole("link", { name: "Previous" })).toHaveAttribute("href", "/alice/hello/commits/main")
})

// Kept on fetch: the mock (like the backend) rejects a commit with no changes, so it cannot produce this.
it("commit with no files", async () => {
  routes[D] = () => json(200, detail([]))
  renderAt(`/alice/hello/commit/${A}`)
  expect(await screen.findByText("no files changed")).toBeInTheDocument()
  expect(main().getByTestId("summary")).toHaveTextContent("0 files changed, 0 additions, 0 deletions")
})
