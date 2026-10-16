import { act, render, screen } from "@testing-library/react"
import { toast } from "sonner"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import { AuthProvider, useAuth } from "./AuthContext"

let auth: ReturnType<typeof useAuth>
function Probe() {
  auth = useAuth()
  return <div>{auth.loading ? "loading" : auth.user ? `user ${auth.user.username}` : "logged out"}</div>
}

function mockMe(loggedIn: boolean) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      loggedIn
        ? ({ status: 200, ok: true, json: async () => ({ id: 1, username: "alice" }) } as Response)
        : ({ status: 401, ok: false, json: async () => ({ error: "authentication required" }) } as Response),
    ),
  )
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
  mockMe(true)
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
  mockMe(false)
  render(<AuthProvider><Probe /></AuthProvider>)
  await screen.findByText("logged out")
  act(() => auth.expireSession())
  expect(errorSpy).not.toHaveBeenCalled()
})
