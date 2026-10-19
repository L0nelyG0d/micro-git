import { act, render, screen } from "@testing-library/react"
import { toast } from "sonner"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import { AuthProvider, useAuth } from "./AuthContext"
import { installMock } from "@/test-utils"
import { MockApiService } from "@/services/MockApiService"

let auth: ReturnType<typeof useAuth>
function Probe() {
  auth = useAuth()
  return <div>{auth.loading ? "loading" : auth.user ? `user ${auth.user.username}` : "logged out"}</div>
}

/** The app's service becomes a mock where alice exists, logged in or not. */
async function mockMe(loggedIn: boolean) {
  const mock = new MockApiService()
  await mock.register("alice", "password1")
  if (loggedIn) await mock.login("alice", "password1")
  installMock(mock)
}

let errorSpy: ReturnType<typeof vi.spyOn>
beforeEach(() => {
  errorSpy = vi.spyOn(toast, "error").mockImplementation(() => "")
})
afterEach(() => {
  errorSpy.mockRestore()
  vi.unstubAllGlobals()
})

it("expireSession logs the user out with one toast, even when called twice in a row", async () => {
  await mockMe(true)
  render(<AuthProvider><Probe /></AuthProvider>)
  await screen.findByText("user alice")
  act(() => {
    auth.expireSession()
    auth.expireSession()
  })
  expect(await screen.findByText("logged out")).toBeInTheDocument()
  expect(errorSpy).toHaveBeenCalledTimes(1)
  expect(errorSpy).toHaveBeenCalledWith("Your session has expired. Log in again.")
})

it("expireSession shows no toast when nobody is logged in", async () => {
  await mockMe(false)
  render(<AuthProvider><Probe /></AuthProvider>)
  await screen.findByText("logged out")
  act(() => auth.expireSession())
  expect(errorSpy).not.toHaveBeenCalled()
})

it("a revalidation answered after a local logout does not bring the user back", async () => {
  let release!: () => void
  const gate = new Promise<void>((r) => (release = r))
  let calls = 0
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => {
      if (++calls === 2) await gate
      return { status: 200, ok: true, json: async () => ({ id: calls === 1 ? 1 : 2, username: "alice" }) } as Response
    }),
  )
  render(<AuthProvider><Probe /></AuthProvider>)
  await screen.findByText("user alice")
  await act(async () => { window.dispatchEvent(new Event("focus")) })
  act(() => auth.expireSession())
  expect(await screen.findByText("logged out")).toBeInTheDocument()
  await act(async () => release())
  expect(screen.getByText("logged out")).toBeInTheDocument()
})
