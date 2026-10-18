import { act, render, screen, waitFor } from "@testing-library/react"
import { MemoryRouter } from "react-router-dom"
import { toast } from "sonner"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import App from "@/App"
import { cacheClear } from "@/lib/fetchCache"

type Answer = { status: number; body?: unknown }
let me: Answer
let list: Answer
let hold: Promise<void> | undefined
let toastSpy: ReturnType<typeof vi.spyOn>
const calls: string[] = []
const repo = (name: string) => ({
  id: 1, owner: "o", name, description: "", default_branch: "main", empty: true,
  created_at: "2026-01-02T15:04:05Z", private: false,
})
const page = (...names: string[]): Answer => ({
  status: 200,
  body: { repos: names.map(repo), page: 1, per_page: 50, has_more: false },
})
const user = (id: number, username: string): Answer => ({ status: 200, body: { id, username } })

beforeEach(() => {
  cacheClear()
  calls.length = 0
  me = user(1, "alice")
  list = page("alice-private")
  hold = undefined
  toastSpy = vi.spyOn(toast, "error").mockImplementation(() => "")
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? "GET"} ${url}`)
      if (url === "/api/repos?page=1" && hold) await hold
      const a = url === "/api/auth/me" ? me : url === "/api/repos?page=1" ? list : { status: 404, body: {} }
      return { status: a.status, ok: a.status < 300, json: async () => a.body, headers: new Headers() } as Response
    }),
  )
})
afterEach(() => {
  toastSpy.mockRestore()
  vi.unstubAllGlobals()
})

const count = (key: string) => calls.filter((c) => c === key).length
const meCalls = () => count("GET /api/auth/me")

async function mount() {
  render(<MemoryRouter initialEntries={["/"]}><App /></MemoryRouter>)
  await screen.findByText(/alice-private/)
}
const focus = () => act(async () => { window.dispatchEvent(new Event("focus")) })

it("focus after another tab logged out clears the cached list and shows the anonymous one", async () => {
  await mount()
  me = { status: 401, body: { error: "authentication required" } }
  list = page("public-one")
  await focus()
  expect(await screen.findByText(/public-one/)).toBeInTheDocument()
  expect(screen.queryByText(/alice-private/)).not.toBeInTheDocument()
})

it("visibilitychange to visible after another tab logged in as someone else refetches the list", async () => {
  await mount()
  me = user(2, "bob")
  list = page("bob-private")
  await act(async () => { document.dispatchEvent(new Event("visibilitychange")) })
  expect(await screen.findByText(/bob-private/)).toBeInTheDocument()
  expect(screen.queryByText(/alice-private/)).not.toBeInTheDocument()
})

it("focus with the same user does not refetch the list", async () => {
  await mount()
  const before = count("GET /api/repos?page=1")
  const meBefore = meCalls()
  await focus()
  await waitFor(() => expect(meCalls()).toBe(meBefore + 1))
  expect(count("GET /api/repos?page=1")).toBe(before)
})

it("focus and visibilitychange firing together cause one /auth/me request", async () => {
  await mount()
  const meBefore = meCalls()
  await act(async () => {
    window.dispatchEvent(new Event("focus"))
    document.dispatchEvent(new Event("visibilitychange"))
  })
  await focus()
  expect(meCalls()).toBe(meBefore + 1)
})

it("a network error while revalidating keeps the current user", async () => {
  await mount()
  ;(fetch as ReturnType<typeof vi.fn>).mockImplementationOnce(async () => { throw new TypeError("Failed to fetch") })
  await focus()
  expect(screen.getByText(/alice-private/)).toBeInTheDocument()
})

it("an expired session found on focus shows the expiry toast once", async () => {
  await mount()
  me = { status: 401, body: { error: "authentication required" } }
  await focus()
  await waitFor(() => expect(toastSpy).toHaveBeenCalledWith("Your session has expired. Log in again."))
  expect(toastSpy).toHaveBeenCalledTimes(1)
})

it("after a user change the first user's rows are gone while the new list is still loading", async () => {
  await mount()
  let release!: () => void
  hold = new Promise<void>((r) => (release = r))
  me = user(2, "bob")
  list = page("bob-private")
  await focus()
  await waitFor(() => expect(count("GET /api/repos?page=1")).toBe(2))
  expect(screen.queryByText(/alice-private/)).not.toBeInTheDocument()
  expect(screen.queryByText(/bob-private/)).not.toBeInTheDocument()
  await act(async () => release())
  expect(await screen.findByText(/bob-private/)).toBeInTheDocument()
})
