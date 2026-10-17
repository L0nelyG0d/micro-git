import React from "react"
import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import App from "./App"

type Handler = (body: unknown) => { status: number; body?: unknown; headers?: Record<string, string> } | Error
let routes: Record<string, Handler>
const calls: string[] = []

function json(status: number, body?: unknown, headers: Record<string, string> = {}) {
  return { status, body, headers }
}

beforeEach(() => {
  calls.length = 0
  routes = { "GET /api/repos?page=1": () => json(200, { repos: [], page: 1, per_page: 50, has_more: false }) }
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const key = `${init?.method ?? "GET"} ${url}`
      calls.push(key)
      expect(init?.credentials).toBe("include")
      const h = routes[key]
      if (!h) throw new Error(`unexpected fetch ${key}`)
      const r = h(init?.body ? JSON.parse(init.body as string) : undefined)
      if (r instanceof Error) throw r
      return {
        status: r.status,
        ok: r.status >= 200 && r.status < 300,
        json: async () => r.body,
        headers: new Headers(r.headers),
      } as Response
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  )
}

const me = () => json(200, { id: 1, username: "alice" })
const loggedOut = () => json(401, { error: "authentication required" })
const emptyRepo = () =>
  json(200, {
    id: 1, owner: "alice", name: "hello", description: "", default_branch: "main",
    empty: true, created_at: "2026-01-02T15:04:05Z",
  })

it("shows Log in and Register when logged out, and /auth/me 401 is not an error", async () => {
  routes["GET /api/auth/me"] = loggedOut
  renderAt("/")
  expect(await screen.findByRole("link", { name: "Log in" })).toBeInTheDocument()
  expect(await screen.findByRole("link", { name: "Register" })).toBeInTheDocument()
  expect(screen.queryByText(/session unavailable/i)).not.toBeInTheDocument()
  expect(localStorage.length + sessionStorage.length).toBe(0)
})

it("shows a loading state, then the username and Log out when logged in", async () => {
  routes["GET /api/auth/me"] = me
  renderAt("/")
  expect(screen.getByRole("status", { name: "Loading session" })).toBeInTheDocument()
  expect(screen.queryByRole("link", { name: "Log in" })).not.toBeInTheDocument()
  expect(await screen.findByText("alice")).toBeInTheDocument()
  expect(await screen.findByRole("button", { name: "Log out" })).toBeInTheDocument()
})

it("shows a non-blocking indication on a 500 from /auth/me", async () => {
  routes["GET /api/auth/me"] = () => json(500, { error: "internal error" })
  renderAt("/")
  expect(await screen.findByText(/session unavailable/i)).toBeInTheDocument()
  expect(await screen.findByRole("heading", { name: "Repositories" })).toBeInTheDocument()
})

it("shows the indication on a network error", async () => {
  routes["GET /api/auth/me"] = () => new TypeError("Failed to fetch")
  renderAt("/")
  expect(await screen.findByText(/session unavailable/i)).toBeInTheDocument()
})

it("logs in and goes back to the page the user came from", async () => {
  routes["GET /api/auth/me"] = loggedOut
  routes["POST /api/auth/login"] = (b) => {
    expect(b).toEqual({ username: "alice", password: "secret123" })
    return me()
  }
  routes["GET /api/repos/alice/hello"] = emptyRepo
  renderAt("/alice/hello")
  await userEvent.click(await screen.findByRole("link", { name: "Log in" }))
  await userEvent.type(await screen.findByLabelText("Username"), "alice")
  await userEvent.type(screen.getByLabelText("Password"), "secret123")
  await userEvent.click(screen.getByRole("button", { name: "Log in" }))
  expect(await screen.findByRole("heading", { name: "alice/hello" })).toBeInTheDocument()
  expect(await within(screen.getByRole("navigation")).findByText("alice")).toBeInTheDocument()
})

it("shows the login error inline, clears the password and re-enables submit", async () => {
  routes["GET /api/auth/me"] = loggedOut
  routes["POST /api/auth/login"] = () => json(401, { error: "invalid username or password" })
  renderAt("/login")
  await screen.findByRole("link", { name: "Register" })
  await userEvent.type(await screen.findByLabelText("Username"), "alice")
  await userEvent.type(screen.getByLabelText("Password"), "wrongpass")
  await userEvent.click(screen.getByRole("button", { name: "Log in" }))
  expect(await screen.findByRole("alert")).toHaveTextContent("invalid username or password")
  expect(await screen.findByLabelText("Password")).toHaveValue("")
  expect(await screen.findByRole("button", { name: "Log in" })).toBeEnabled()
})

it("shows a friendly wait message when login is rate limited", async () => {
  routes["GET /api/auth/me"] = loggedOut
  routes["POST /api/auth/login"] = () =>
    json(429, { error: "too many attempts, try again later" }, { "Retry-After": "90" })
  renderAt("/login")
  await screen.findByRole("link", { name: "Register" })
  await userEvent.type(await screen.findByLabelText("Username"), "alice")
  await userEvent.type(screen.getByLabelText("Password"), "wrongpass")
  await userEvent.click(screen.getByRole("button", { name: "Log in" }))
  expect(await screen.findByRole("alert")).toHaveTextContent("Too many attempts. Please wait 2 minutes and try again.")
  expect(await screen.findByRole("button", { name: "Log in" })).toBeEnabled()
})

it("disables submit while the request is in flight", async () => {
  routes["GET /api/auth/me"] = loggedOut
  let release!: () => void
  const gate = new Promise<void>((r) => (release = r))
  routes["POST /api/auth/login"] = () => me()
  const inner = vi.mocked(fetch).getMockImplementation()!
  vi.mocked(fetch).mockImplementation(async (u, i) => {
    if (i?.method === "POST") await gate
    return inner(u, i)
  })
  renderAt("/login")
  await screen.findByRole("link", { name: "Register" })
  await userEvent.type(await screen.findByLabelText("Username"), "alice")
  await userEvent.type(screen.getByLabelText("Password"), "secret123")
  await userEvent.click(screen.getByRole("button", { name: "Log in" }))
  expect(screen.getByRole("button", { name: "Log in" })).toBeDisabled()
  release()
  await screen.findByRole("heading", { name: "Repositories" })
})

it("registers then logs in, in that order", async () => {
  routes["GET /api/auth/me"] = loggedOut
  routes["POST /api/auth/register"] = () => json(201, { id: 1, username: "alice" })
  routes["POST /api/auth/login"] = () => me()
  renderAt("/register")
  await screen.findByRole("link", { name: "Log in" })
  await userEvent.type(await screen.findByLabelText("Username"), "Alice")
  await userEvent.type(screen.getByLabelText("Password"), "secret123")
  await userEvent.click(screen.getByRole("button", { name: "Register" }))
  expect(await screen.findByText("alice")).toBeInTheDocument()
  expect(calls.filter((c) => c.startsWith("POST"))).toEqual(["POST /api/auth/register", "POST /api/auth/login"])
  expect(await screen.findByRole("heading", { name: "Repositories" })).toBeInTheDocument()
})

it("shows a 409 on register inline", async () => {
  routes["GET /api/auth/me"] = loggedOut
  routes["POST /api/auth/register"] = () => json(409, { error: "username already taken" })
  renderAt("/register")
  await screen.findByRole("link", { name: "Log in" })
  await userEvent.type(await screen.findByLabelText("Username"), "alice")
  await userEvent.type(screen.getByLabelText("Password"), "secret123")
  await userEvent.click(screen.getByRole("button", { name: "Register" }))
  expect(await screen.findByRole("alert")).toHaveTextContent("username already taken")
})

it("sends the user to /login with a message if login after register fails", async () => {
  routes["GET /api/auth/me"] = loggedOut
  routes["POST /api/auth/register"] = () => json(201, { id: 1, username: "alice" })
  routes["POST /api/auth/login"] = () => json(500, { error: "internal error" })
  renderAt("/register")
  await screen.findByRole("link", { name: "Log in" })
  await userEvent.type(await screen.findByLabelText("Username"), "alice")
  await userEvent.type(screen.getByLabelText("Password"), "secret123")
  await userEvent.click(screen.getByRole("button", { name: "Register" }))
  expect(await screen.findByRole("heading", { name: "Log in" })).toBeInTheDocument()
  expect(await screen.findByRole("status", { name: "" })).toHaveTextContent(/account was created/i)
})

it("redirects a logged-in user from /login and /register to /", async () => {
  routes["GET /api/auth/me"] = me
  renderAt("/login")
  expect(await screen.findByRole("heading", { name: "Repositories" })).toBeInTheDocument()
})

it("logs out, clears the session and goes to /", async () => {
  routes["GET /api/auth/me"] = me
  routes["POST /api/auth/logout"] = () => json(204)
  routes["GET /api/repos/alice/hello"] = emptyRepo
  renderAt("/alice/hello")
  await userEvent.click(await screen.findByRole("button", { name: "Log out" }))
  expect(await screen.findByRole("link", { name: "Log in" })).toBeInTheDocument()
  expect(await screen.findByRole("heading", { name: "Repositories" })).toBeInTheDocument()
})

it("stays logged in and shows an error when logout fails", async () => {
  routes["GET /api/auth/me"] = me
  routes["POST /api/auth/logout"] = () => json(500, { error: "internal error" })
  routes["GET /api/repos/alice/hello"] = emptyRepo
  renderAt("/alice/hello")
  await userEvent.click(await screen.findByRole("button", { name: "Log out" }))
  expect(await screen.findByText(/could not log out/i)).toBeInTheDocument()
  expect(await within(screen.getByRole("navigation")).findByText("alice")).toBeInTheDocument()
  expect(await screen.findByRole("heading", { name: "alice/hello" })).toBeInTheDocument()
})

it("shows page not found with a link home for unknown URLs", async () => {
  routes["GET /api/auth/me"] = loggedOut
  renderAt("/nope/nope/nope/nope/nope/nope")
  expect(await screen.findByRole("heading", { name: "Page not found" })).toBeInTheDocument()
  expect(await screen.findByRole("link", { name: "Go home" })).toHaveAttribute("href", "/")
})

it.each([
  ["/", "micro git"],
  ["/alice/hello", "alice/hello · micro git"],
  ["/alice/hello/commits/main", "History · alice/hello · micro git"],
  ["/alice/hello/commit/1234567abcdef", "Commit 1234567 · alice/hello · micro git"],
  ["/alice/hello/compare", "Compare · alice/hello · micro git"],
  ["/alice/hello/blob/main/src/a.txt", "src/a.txt · alice/hello · micro git"],
  ["/alice/hello/new/main", "New file · alice/hello · micro git"],
  ["/login", "Log in · micro git"],
  ["/register", "Register · micro git"],
  ["/nope/nope/nope/nope/nope/nope", "Page not found · micro git"],
])("sets the document title for %s", async (path, title) => {
  routes["GET /api/auth/me"] = loggedOut
  renderAt(path)
  await waitFor(() => expect(document.title).toBe(title))
})

it("has a skip link as the first tab stop that focuses main", async () => {
  routes["GET /api/auth/me"] = loggedOut
  renderAt("/")
  await screen.findByRole("link", { name: "Register" })
  await userEvent.tab()
  expect(screen.getByRole("link", { name: "Skip to main content" })).toHaveFocus()
  await userEvent.keyboard("{Enter}")
  expect(screen.getByRole("main")).toHaveFocus()
})

it("under StrictMode does not focus main on first load and the skip link is the first tab stop", async () => {
  routes["GET /api/auth/me"] = loggedOut
  render(
    <React.StrictMode>
      <MemoryRouter initialEntries={["/"]}>
        <App />
      </MemoryRouter>
    </React.StrictMode>,
  )
  await screen.findByRole("link", { name: "Register" })
  expect(screen.getByRole("main")).not.toHaveFocus()
  await userEvent.tab()
  expect(screen.getByRole("link", { name: "Skip to main content" })).toHaveFocus()
})

it("moves focus to main after a client-side navigation but not on first load", async () => {
  routes["GET /api/auth/me"] = loggedOut
  renderAt("/")
  await screen.findByRole("link", { name: "Register" })
  expect(screen.getByRole("main")).not.toHaveFocus()
  await userEvent.click(screen.getByRole("link", { name: "Register" }))
  await waitFor(() => expect(screen.getByRole("main")).toHaveFocus())
})
