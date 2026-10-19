import { renderWithMock } from "@/test-mock"
import { MockApiService } from "@/services/MockApiService"
import { act, screen, waitFor } from "@testing-library/react"
import { toast } from "sonner"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import { cacheClear } from "@/lib/fetchCache"

let toastSpy: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  cacheClear()
  toastSpy = vi.spyOn(toast, "error").mockImplementation(() => "")
})
afterEach(() => toastSpy.mockRestore())

const focus = () => act(async () => { window.dispatchEvent(new Event("focus")) })

/** alice (logged in) owns a private and a public repo; bob owns one private repo. */
async function twoUsers() {
  const mock = new MockApiService()
  await mock.register("alice", "password1")
  await mock.register("bob", "password1")
  await mock.login("bob", "password1")
  await mock.createRepo({ name: "bob-private", private: true })
  await mock.login("alice", "password1")
  await mock.createRepo({ name: "alice-private", private: true })
  await mock.createRepo({ name: "public-one" })
  return mock
}
// Another tab changing the session is mock.logout() / mock.login() on the same instance.
async function mountMock() {
  const mock = await twoUsers()
  renderWithMock("/", mock)
  await screen.findByText(/alice-private/)
  return mock
}

it("focus after another tab logged out clears the cached list and shows the anonymous one", async () => {
  const mock = await mountMock()
  await mock.logout()
  await focus()
  expect(await screen.findByText(/public-one/)).toBeInTheDocument()
  expect(screen.queryByText(/alice-private/)).not.toBeInTheDocument()
})

it("visibilitychange to visible after another tab logged in as someone else refetches the list", async () => {
  const mock = await mountMock()
  await mock.login("bob", "password1")
  await act(async () => { document.dispatchEvent(new Event("visibilitychange")) })
  expect(await screen.findByText(/bob-private/)).toBeInTheDocument()
  expect(screen.queryByText(/alice-private/)).not.toBeInTheDocument()
})

it("focus with the same user does not refetch the list", async () => {
  const mock = await mountMock()
  const me = vi.spyOn(mock, "me")
  const list = vi.spyOn(mock, "listRepos")
  await focus()
  await waitFor(() => expect(me).toHaveBeenCalledTimes(1))
  expect(list).not.toHaveBeenCalled()
})

it("focus and visibilitychange firing together cause one /auth/me request", async () => {
  const mock = await mountMock()
  const me = vi.spyOn(mock, "me")
  await act(async () => {
    window.dispatchEvent(new Event("focus"))
    document.dispatchEvent(new Event("visibilitychange"))
  })
  await focus()
  expect(me).toHaveBeenCalledTimes(1)
})

// The mock has no failure controls, so the network error is injected on the instance with a spy (the mock itself is unchanged).
it("a network error while revalidating keeps the current user", async () => {
  const mock = await mountMock()
  vi.spyOn(mock, "me").mockRejectedValueOnce(new TypeError("Failed to fetch"))
  await focus()
  expect(screen.getByText(/alice-private/)).toBeInTheDocument()
})

it("an expired session found on focus shows the expiry toast once", async () => {
  const mock = await mountMock()
  await mock.logout()
  await focus()
  await waitFor(() => expect(toastSpy).toHaveBeenCalledWith("Your session has expired. Log in again."))
  expect(toastSpy).toHaveBeenCalledTimes(1)
})

it("after a user change the first user's rows are gone while the new list is still loading", async () => {
  const mock = await mountMock()
  let release!: () => void
  const hold = new Promise<void>((r) => (release = r))
  const real = mock.listRepos.bind(mock)
  const list = vi.spyOn(mock, "listRepos").mockImplementation(async (...args) => {
    await hold
    return real(...args)
  })
  await mock.login("bob", "password1") // another tab switched the user
  await focus()
  await waitFor(() => expect(list).toHaveBeenCalledTimes(1))
  expect(screen.queryByText(/alice-private/)).not.toBeInTheDocument()
  expect(screen.queryByText(/bob-private/)).not.toBeInTheDocument()
  await act(async () => release())
  expect(await screen.findByText(/bob-private/)).toBeInTheDocument()
})
