import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useLocation } from "react-router-dom"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import App from "@/App"
import { MemoryRouter, nextTask } from "@/test-utils"
import { cacheClear } from "@/lib/fetchCache"

type Res = { status: number; body?: unknown } | Error
let routes: Record<string, () => Res | Promise<Res>>
let calls: string[]

const json = (status: number, body?: unknown): Res => ({ status, body })
const A = "a".repeat(40)
const B = "b".repeat(40)
const API = "GET /api/repos/alice/hello"
const repo = (o: Record<string, unknown> = {}) => ({
  id: 1, owner: "alice", name: "hello", description: "", default_branch: "main",
  empty: false, created_at: "2026-01-02T15:04:05Z", ...o,
})
const branches = {
  branches: [
    { name: "dev.1", oid: B, is_default: false },
    { name: "main", oid: A, is_default: true },
  ],
}
const cmp = (files: unknown[]) => ({ base: "main", head: "dev.1", base_commit: A, head_commit: B, files })
const file = (o: Record<string, unknown>) => ({
  path: "f.txt", status: "modified", additions: 0, deletions: 0, truncated: false, hunks: [], ...o,
})
const Q = `${API}/compare?base=main&head=dev.1`

beforeEach(() => {
  cacheClear()
  calls = []
  routes = {
    "GET /api/auth/me": () => json(401, { error: "authentication required" }),
    [API]: () => json(200, repo()),
    [`${API}/branches`]: () => json(200, branches),
  }
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    const key = `${init?.method ?? "GET"} ${url}`
    calls.push(key)
    await nextTask()
    const h = routes[key]
    if (!h) throw new Error(`unexpected fetch ${key}`)
    const r = await h()
    if (r instanceof Error) throw r
    return { status: r.status, ok: r.status < 300, json: async () => r.body } as Response
  }))
})
afterEach(() => vi.unstubAllGlobals())

function Loc() {
  const l = useLocation()
  return <div data-testid="loc">{l.pathname + l.search}</div>
}
const renderAt = (p: string) =>
  render(<MemoryRouter initialEntries={[p]}><App /><Loc /></MemoryRouter>)
const main = () => within(screen.getByRole("main"))

it("renders summary and the shared diff, prefilled selects, truncated note", async () => {
  // Hold the repo answer so the loading state is observable (requests start in parallel and are fast).
  let release: () => void = () => {}
  const gate = new Promise<void>((r) => (release = r))
  const repoRoute = routes[API]
  routes[API] = async () => {
    await gate
    return repoRoute()
  }
  routes[Q] = () => json(200, cmp([
    file({ path: "a.txt", additions: 1, deletions: 1, hunks: [{ old_start: 1, old_lines: 1, new_start: 1, new_lines: 1,
      lines: [{ type: "del", text: "old" }, { type: "add", text: "new" }] }] }),
    file({ path: "big.txt", status: "added", additions: 3, truncated: true }),
  ]))
  renderAt("/alice/hello/compare?base=main&head=dev.1")
  expect(await main().findByRole("status", { name: "Loading repository" })).toBeInTheDocument()
  release()
  expect(await screen.findByTestId("summary")).toHaveTextContent("2 files changed, +4 -1")
  expect(screen.getByRole("combobox", { name: "Base" })).toHaveTextContent("main")
  expect(screen.getByRole("combobox", { name: "Compare" })).toHaveTextContent("dev.1")
  expect(screen.getByTestId("line-add")).toHaveTextContent("+new")
  expect(screen.getByTestId("line-del")).toHaveTextContent("-old")
  expect(screen.getByText("diff too large to display")).toBeInTheDocument()
  expect(screen.getByRole("link", { name: "alice/hello" })).toHaveClass("max-sm:break-all")
})

it("shows the empty state for identical tips", async () => {
  routes[Q] = () => json(200, cmp([]))
  renderAt("/alice/hello/compare?base=main&head=dev.1")
  expect(await screen.findByText("No differences between these branches")).toBeInTheDocument()
  expect(screen.queryByTestId("summary")).not.toBeInTheDocument()
})

it("changing a select updates the URL and reloads", async () => {
  routes[Q] = () => json(200, cmp([]))
  routes[`${API}/compare?base=dev.1&head=dev.1`] = () => json(200, cmp([file({ path: "z.txt", additions: 1 })]))
  renderAt("/alice/hello/compare?base=main&head=dev.1")
  await userEvent.click(await screen.findByRole("combobox", { name: "Base" }))
  await userEvent.click(screen.getByRole("option", { name: "dev.1" }))
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/compare?base=dev.1&head=dev.1")
  expect(await screen.findByTestId("summary")).toHaveTextContent("1 file changed, +1 -0")
})

it("shows an error with retry", async () => {
  let n = 0
  routes[Q] = () => (n++ === 0 ? json(500, { error: "boom" }) : json(200, cmp([])))
  renderAt("/alice/hello/compare?base=main&head=dev.1")
  expect(await screen.findByRole("alert")).toHaveTextContent("boom")
  await userEvent.click(screen.getByRole("button", { name: "Retry" }))
  expect(await screen.findByText("No differences between these branches")).toBeInTheDocument()
})

it("shows the empty-repo hint without calling compare", async () => {
  routes[API] = () => json(200, repo({ empty: true }))
  renderAt("/alice/hello/compare")
  expect(await screen.findByText(/This repository is empty/)).toBeInTheDocument()
  expect(calls.some((c) => c.includes("/compare"))).toBe(false)
})

it("repo page links to compare", async () => {
  routes[`${API}/tree/main`] = () => json(200, { ref: "main", commit: A, path: "", entries: [] })
  routes[`${API}/commits?ref=main&page=1`] = () => json(200, { commits: [] })
  renderAt("/alice/hello")
  expect(await screen.findByRole("link", { name: "Compare" })).toHaveAttribute("href", "/alice/hello/compare")
})
