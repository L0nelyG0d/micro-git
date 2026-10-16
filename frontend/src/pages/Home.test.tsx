import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import App from "@/App"
import { formatDate } from "@/lib/format"

type Res = { status: number; body?: unknown } | Error
let routes: Record<string, (body: unknown) => Res | Promise<Res>>
let calls: string[]

const json = (status: number, body?: unknown): Res => ({ status, body })
const me = () => json(200, { id: 1, username: "alice" })
const loggedOut = () => json(401, { error: "authentication required" })
const repo = (o: Partial<Record<string, unknown>> = {}) => ({
  id: 1,
  owner: "alice",
  name: "hello",
  description: "A greeting",
  default_branch: "main",
  empty: true,
  created_at: "2026-01-02T15:04:05Z",
  ...o,
})

beforeEach(() => {
  calls = []
  routes = {}
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const key = `${init?.method ?? "GET"} ${url}`
      calls.push(key)
      expect(init?.credentials).toBe("include")
      const h = routes[key]
      if (!h) throw new Error(`unexpected fetch ${key}`)
      const r = await h(init?.body ? JSON.parse(init.body as string) : undefined)
      if (r instanceof Error) throw r
      return { status: r.status, ok: r.status < 300, json: async () => r.body } as Response
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

function renderHome() {
  return render(
    <MemoryRouter initialEntries={["/"]}>
      <App />
    </MemoryRouter>,
  )
}

async function openForm() {
  await userEvent.click(await screen.findByRole("button", { name: "New repository" }))
}

it("lists repos in API order with links, description and formatted date", async () => {
  routes["GET /api/auth/me"] = loggedOut
  routes["GET /api/repos"] = () =>
    json(200, {
      repos: [
        repo({ id: 2, name: "second", description: "" }),
        repo({ id: 1, name: "first", owner: "bob" }),
      ],
    })
  renderHome()
  const links = await screen.findAllByRole("link", { name: /^(alice|bob)\// })
  expect(links.map((l) => l.textContent)).toEqual(["alice/second", "bob/first"])
  expect(links[0]).toHaveAttribute("href", "/alice/second")
  expect(screen.getByText("A greeting")).toBeInTheDocument()
  expect(screen.getAllByText(formatDate("2026-01-02T15:04:05Z"))).toHaveLength(2)
  expect(formatDate("2026-01-02T15:04:05Z")).toMatch(/^\d{2}\.\d{2}\.\d{4} \d{2}:\d{2}$/)
  expect(document.body).not.toHaveTextContent(/null|undefined/)
})

it("shows a loading state while the request is pending", async () => {
  routes["GET /api/auth/me"] = loggedOut
  routes["GET /api/repos"] = () => new Promise<Res>(() => {})
  renderHome()
  expect(await screen.findByRole("status", { name: "Loading repositories" })).toBeInTheDocument()
})

it("shows the empty state pointing at login when logged out", async () => {
  routes["GET /api/auth/me"] = loggedOut
  routes["GET /api/repos"] = () => json(200, { repos: [] })
  renderHome()
  expect(await screen.findByText(/no repositories yet/i)).toBeInTheDocument()
  const main = screen.getByRole("main")
  expect(within(main).getAllByRole("link", { name: /log in/i }).length).toBeGreaterThan(0)
  expect(screen.queryByRole("button", { name: "New repository" })).not.toBeInTheDocument()
})

it("shows the empty state pointing at New repository when logged in", async () => {
  routes["GET /api/auth/me"] = me
  routes["GET /api/repos"] = () => json(200, { repos: [] })
  renderHome()
  expect(await screen.findByText(/no repositories yet.*new repository/i)).toBeInTheDocument()
  expect(await screen.findByRole("button", { name: "New repository" })).toBeInTheDocument()
})

it("shows an error with Retry for a 500 and reloads on retry", async () => {
  routes["GET /api/auth/me"] = loggedOut
  routes["GET /api/repos"] = () => json(500, { error: "internal error" })
  renderHome()
  expect(await screen.findByRole("alert")).toHaveTextContent("internal error")
  routes["GET /api/repos"] = () => json(200, { repos: [repo()] })
  await userEvent.click(screen.getByRole("button", { name: "Retry" }))
  expect(await screen.findByRole("link", { name: "alice/hello" })).toBeInTheDocument()
  expect(screen.queryByRole("alert")).not.toBeInTheDocument()
})

it("shows an error with Retry for a network error", async () => {
  routes["GET /api/auth/me"] = loggedOut
  routes["GET /api/repos"] = () => new TypeError("Failed to fetch")
  renderHome()
  expect(await screen.findByRole("alert")).toHaveTextContent(/could not reach the server/i)
  expect(await screen.findByRole("button", { name: "Retry" })).toBeInTheDocument()
})

it("creates a repo: toast, form closed, list updated without reload", async () => {
  routes["GET /api/auth/me"] = me
  routes["GET /api/repos"] = () => json(200, { repos: [repo({ id: 1, name: "old" })] })
  routes["POST /api/repos"] = (b) => {
    expect(b).toEqual({ name: "new-one", description: "Hi" })
    return json(201, repo({ id: 2, name: "new-one", description: "Hi" }))
  }
  renderHome()
  await openForm()
  await userEvent.type(screen.getByLabelText("Name"), "new-one")
  await userEvent.type(screen.getByLabelText(/Description/), "Hi")
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  expect(await screen.findByText(/alice\/new-one created/i)).toBeInTheDocument()
  expect(screen.queryByRole("form", { name: "New repository" })).not.toBeInTheDocument()
  const links = screen.getAllByRole("link", { name: /^alice\// })
  expect(links.map((l) => l.textContent)).toEqual(["alice/new-one", "alice/old"])
  expect(calls.filter((c) => c === "GET /api/repos")).toHaveLength(1)
  // reopening gives a fresh form
  await openForm()
  expect(screen.getByLabelText("Name")).toHaveValue("")
})

it("validates client-side without calling the server", async () => {
  routes["GET /api/auth/me"] = me
  routes["GET /api/repos"] = () => json(200, { repos: [] })
  renderHome()
  await openForm()
  const submit = screen.getByRole("button", { name: "Create repository" })
  await userEvent.click(submit)
  expect(await screen.findByRole("alert")).toHaveTextContent(/enter a repository name/i)
  await userEvent.type(screen.getByLabelText("Name"), "..")
  await userEvent.click(submit)
  expect(await screen.findByRole("alert")).toHaveTextContent(/cannot be \. or \.\./i)
  await userEvent.clear(screen.getByLabelText("Name"))
  await userEvent.type(screen.getByLabelText("Name"), "bad name")
  await userEvent.click(submit)
  expect(await screen.findByRole("alert")).toHaveTextContent(/letters, digits/i)
  await userEvent.clear(screen.getByLabelText("Name"))
  await userEvent.type(screen.getByLabelText("Name"), "ok")
  await userEvent.click(screen.getByLabelText(/Description/))
  await userEvent.paste("x".repeat(501))
  await userEvent.click(submit)
  expect(await screen.findByRole("alert")).toHaveTextContent(/at most 500/i)
  expect(calls).not.toContain("POST /api/repos")
})

it("shows 409 inline exactly as returned and keeps the input", async () => {
  routes["GET /api/auth/me"] = me
  routes["GET /api/repos"] = () => json(200, { repos: [] })
  routes["POST /api/repos"] = () => json(409, { error: "repository already exists" })
  renderHome()
  await openForm()
  await userEvent.type(screen.getByLabelText("Name"), "hello")
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  expect(await screen.findByRole("alert")).toHaveTextContent("repository already exists")
  expect(screen.getByLabelText("Name")).toHaveValue("hello")
  expect(screen.getByRole("button", { name: "Create repository" })).toBeEnabled()
})

it("shows server 400 messages inline", async () => {
  routes["GET /api/auth/me"] = me
  routes["GET /api/repos"] = () => json(200, { repos: [] })
  routes["POST /api/repos"] = () => json(400, { error: "invalid repository name" })
  renderHome()
  await openForm()
  await userEvent.type(screen.getByLabelText("Name"), "hello")
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  expect(await screen.findByRole("alert")).toHaveTextContent("invalid repository name")
  routes["POST /api/repos"] = () =>
    json(400, { error: "description must be at most 500 characters" })
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "description must be at most 500 characters",
  )
})

it("on 401 shows a message and the nav goes to the logged-out state", async () => {
  routes["GET /api/auth/me"] = me
  routes["GET /api/repos"] = () => json(200, { repos: [] })
  routes["POST /api/repos"] = () => json(401, { error: "authentication required" })
  renderHome()
  await openForm()
  await userEvent.type(screen.getByLabelText("Name"), "hello")
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  expect(await screen.findByText(/session has expired/i)).toBeInTheDocument()
  expect(await screen.findByRole("link", { name: "Register" })).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Log out" })).not.toBeInTheDocument()
})

it("disables submit in flight and discards input on cancel", async () => {
  routes["GET /api/auth/me"] = me
  routes["GET /api/repos"] = () => json(200, { repos: [] })
  routes["POST /api/repos"] = () => new Promise<Res>(() => {})
  renderHome()
  await openForm()
  await userEvent.type(screen.getByLabelText("Name"), "hello")
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  expect(screen.getByRole("button", { name: "Create repository" })).toBeDisabled()
  await userEvent.click(screen.getByRole("button", { name: "Cancel" }))
  await openForm()
  expect(screen.getByLabelText("Name")).toHaveValue("")
})
