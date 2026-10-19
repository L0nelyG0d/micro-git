import { MemoryRouter } from "@/test-utils"
import { ApiError } from "@/services/http"
import { failNext, holdNext, installMock, networkErrorNext, renderWithMock } from "@/test-mock"
import { MockApiService } from "@/services/MockApiService"
import React from "react"
import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, it, vi } from "vitest"
import App from "./App"

/** A mock with alice (and her empty repo hello), logged in or not. */
async function aliceMock(loggedIn: boolean) {
  const mock = new MockApiService()
  await mock.register("alice", "secret123")
  await mock.login("alice", "secret123")
  await mock.createRepo({ name: "hello" })
  if (!loggedIn) await mock.logout()
  return mock
}

it("shows Log in and Register when logged out, and /auth/me 401 is not an error", async () => {
  renderWithMock("/", await aliceMock(false))
  expect(await screen.findByRole("link", { name: "Log in" })).toBeInTheDocument()
  expect(await screen.findByRole("link", { name: "Register" })).toBeInTheDocument()
  expect(screen.queryByText(/session unavailable/i)).not.toBeInTheDocument()
  expect(localStorage.length + sessionStorage.length).toBe(0)
})

it("shows a loading state, then the username and Log out when logged in", async () => {
  renderWithMock("/", await aliceMock(true))
  expect(screen.getByRole("status", { name: "Loading session" })).toBeInTheDocument()
  expect(screen.queryByRole("link", { name: "Log in" })).not.toBeInTheDocument()
  expect(await screen.findByText("alice")).toBeInTheDocument()
  expect(await screen.findByRole("button", { name: "Log out" })).toBeInTheDocument()
})

it("logs in and goes back to the page the user came from", async () => {
  renderWithMock("/alice/hello", await aliceMock(false))
  await userEvent.click(await screen.findByRole("link", { name: "Log in" }))
  await userEvent.type(await screen.findByLabelText("Username"), "alice")
  await userEvent.type(screen.getByLabelText("Password"), "secret123")
  await userEvent.click(screen.getByRole("button", { name: "Log in" }))
  expect(await screen.findByRole("heading", { name: "alice/hello" })).toBeInTheDocument()
  expect(await within(screen.getByRole("navigation", { name: "Main" })).findByText("alice")).toBeInTheDocument()
})

it("shows the login error inline, clears the password and re-enables submit", async () => {
  renderWithMock("/login", await aliceMock(false))
  await screen.findByRole("link", { name: "Register" })
  await userEvent.type(await screen.findByLabelText("Username"), "alice")
  await userEvent.type(screen.getByLabelText("Password"), "wrongpass")
  await userEvent.click(screen.getByRole("button", { name: "Log in" }))
  expect(await screen.findByRole("alert")).toHaveTextContent("invalid username or password")
  expect(await screen.findByLabelText("Password")).toHaveValue("")
  expect(await screen.findByRole("button", { name: "Log in" })).toBeEnabled()
})

it("registers then logs in, in that order", async () => {
  const mock = new MockApiService()
  renderWithMock("/register", mock)
  // Scoped to <main>: the lazy Register page is what we wait for; the nav also has a "Log in" link.
  await within(screen.getByRole("main")).findByRole("link", { name: "Log in" })
  await userEvent.type(await screen.findByLabelText("Username"), "Alice")
  await userEvent.type(screen.getByLabelText("Password"), "secret123")
  await userEvent.click(screen.getByRole("button", { name: "Register" }))
  expect(await screen.findByText("alice")).toBeInTheDocument()
  // Register itself starts no session: the user is logged in only because the page logged in next.
  expect(await mock.me()).toMatchObject({ username: "alice" })
  expect(await screen.findByRole("heading", { name: "Repositories" })).toBeInTheDocument()
})

it("shows a 409 on register inline", async () => {
  renderWithMock("/register", await aliceMock(false))
  // Scoped to <main>: the lazy Register page is what we wait for; the nav also has a "Log in" link.
  await within(screen.getByRole("main")).findByRole("link", { name: "Log in" })
  await userEvent.type(await screen.findByLabelText("Username"), "alice")
  await userEvent.type(screen.getByLabelText("Password"), "secret123")
  await userEvent.click(screen.getByRole("button", { name: "Register" }))
  expect(await screen.findByRole("alert")).toHaveTextContent("username already taken")
})

it("redirects a logged-in user from /login and /register to /", async () => {
  renderWithMock("/login", await aliceMock(true))
  expect(await screen.findByRole("heading", { name: "Repositories" })).toBeInTheDocument()
})

it("logs out, clears the session and goes to /", async () => {
  const mock = await aliceMock(true)
  renderWithMock("/alice/hello", mock)
  await userEvent.click(await screen.findByRole("button", { name: "Log out" }))
  expect(await screen.findByRole("link", { name: "Log in" })).toBeInTheDocument()
  expect(await screen.findByRole("heading", { name: "Repositories" })).toBeInTheDocument()
  await expect(mock.me()).rejects.toMatchObject({ status: 401 })
})

it("shows page not found with a link home for unknown URLs", async () => {
  renderWithMock("/nope/nope/nope/nope/nope/nope", await aliceMock(false))
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
  renderWithMock(path, await aliceMock(false))
  await waitFor(() => expect(document.title).toBe(title))
})

it("has a skip link as the first tab stop that focuses main", async () => {
  renderWithMock("/", await aliceMock(false))
  await screen.findByRole("link", { name: "Register" })
  await userEvent.tab()
  expect(screen.getByRole("link", { name: "Skip to main content" })).toHaveFocus()
  await userEvent.keyboard("{Enter}")
  expect(screen.getByRole("main")).toHaveFocus()
})

it("under StrictMode does not focus main on first load and the skip link is the first tab stop", async () => {
  installMock(await aliceMock(false))
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
  renderWithMock("/", await aliceMock(false))
  await screen.findByRole("link", { name: "Register" })
  expect(screen.getByRole("main")).not.toHaveFocus()
  await userEvent.click(screen.getByRole("link", { name: "Register" }))
  await waitFor(() => expect(screen.getByRole("main")).toHaveFocus())
})

it("focuses the first empty field after a failed submit and links it to its error", async () => {
  renderWithMock("/login", await aliceMock(false))
  await screen.findByRole("link", { name: "Register" })
  await userEvent.click(await screen.findByRole("button", { name: "Log in" }))
  const username = screen.getByLabelText("Username")
  expect(username).toHaveFocus()
  expect(username).toHaveAttribute("aria-invalid", "true")
  expect(username).toHaveAttribute("aria-describedby", screen.getByRole("alert").id)
  expect(username).toHaveAttribute("spellcheck", "false")
  await userEvent.type(username, "alice")
  await userEvent.click(screen.getByRole("button", { name: "Log in" }))
  expect(screen.getByLabelText("Password")).toHaveFocus()
  expect(screen.getByLabelText("Password")).toHaveAttribute("aria-invalid", "true")
  expect(username).not.toHaveAttribute("aria-invalid", "true")
})

it("focuses the password and ties it to the alert after a rejected login", async () => {
  renderWithMock("/login", await aliceMock(false))
  await screen.findByRole("link", { name: "Register" })
  await userEvent.type(await screen.findByLabelText("Username"), "alice")
  await userEvent.type(screen.getByLabelText("Password"), "wrongpass")
  await userEvent.click(screen.getByRole("button", { name: "Log in" }))
  const alert = await screen.findByRole("alert")
  expect(screen.getByLabelText("Password")).toHaveFocus()
  expect(screen.getByLabelText("Password")).toHaveAttribute("aria-describedby", alert.id)
})

it("focuses the username and marks only it invalid when registering a taken username (409)", async () => {
  renderWithMock("/register", await aliceMock(false))
  await within(screen.getByRole("main")).findByRole("link", { name: "Log in" })
  await userEvent.type(await screen.findByLabelText("Username"), "alice")
  await userEvent.type(screen.getByLabelText("Password"), "secret123")
  await userEvent.click(screen.getByRole("button", { name: "Register" }))
  const alert = await screen.findByRole("alert")
  const username = screen.getByLabelText("Username")
  expect(username).toHaveFocus()
  expect(username).toHaveAttribute("aria-invalid", "true")
  expect(username).toHaveAttribute("aria-describedby", alert.id)
  expect(screen.getByLabelText("Password")).not.toHaveAttribute("aria-invalid", "true")
})

it("shows a non-blocking indication on a 500 from /auth/me", async () => {
  const mock = await aliceMock(true)
  failNext(mock, "me")
  renderWithMock("/", mock)
  expect(await screen.findByText(/session unavailable/i)).toBeInTheDocument()
  expect(await screen.findByRole("heading", { name: "Repositories" })).toBeInTheDocument()
})

it("shows the indication on a network error", async () => {
  const mock = await aliceMock(true)
  networkErrorNext(mock, "me")
  renderWithMock("/", mock)
  expect(await screen.findByText(/session unavailable/i)).toBeInTheDocument()
})

it.each([
  ["/login", "Log in", "login"],
  ["/register", "Register", "register"],
] as const)("shows the unreachable text, not the browser's, on a network failure at %s", async (path, button, method) => {
  const mock = await aliceMock(false)
  networkErrorNext(mock, method)
  renderWithMock(path, mock)
  await within(screen.getByRole("main")).findByRole("button", { name: button })
  await userEvent.type(await screen.findByLabelText("Username"), "alice")
  await userEvent.type(screen.getByLabelText("Password"), "secret123")
  await userEvent.click(screen.getByRole("button", { name: button }))
  const alert = await screen.findByRole("alert")
  expect(alert).toHaveTextContent("Could not reach the server. Check your connection and try again.")
  expect(alert).not.toHaveTextContent(/failed to fetch/i)
})

it("shows a friendly wait message when login is rate limited", async () => {
  const mock = await aliceMock(false)
  vi.spyOn(mock, "login").mockRejectedValueOnce(new ApiError(429, "too many attempts, try again later", 90))
  renderWithMock("/login", mock)
  await screen.findByRole("link", { name: "Register" })
  await userEvent.type(await screen.findByLabelText("Username"), "alice")
  await userEvent.type(screen.getByLabelText("Password"), "wrongpass")
  await userEvent.click(screen.getByRole("button", { name: "Log in" }))
  expect(await screen.findByRole("alert")).toHaveTextContent("Too many attempts. Please wait 2 minutes and try again.")
  expect(await screen.findByRole("button", { name: "Log in" })).toBeEnabled()
})

it("disables submit while the request is in flight", async () => {
  const mock = await aliceMock(false)
  const release = holdNext(mock, "login")
  renderWithMock("/login", mock)
  await screen.findByRole("link", { name: "Register" })
  await userEvent.type(await screen.findByLabelText("Username"), "alice")
  await userEvent.type(screen.getByLabelText("Password"), "secret123")
  await userEvent.click(screen.getByRole("button", { name: "Log in" }))
  expect(screen.getByRole("button", { name: "Log in" })).toBeDisabled()
  release()
  await screen.findByRole("heading", { name: "Repositories" })
})

it("sends the user to /login with a message if login after register fails", async () => {
  const mock = new MockApiService()
  failNext(mock, "login")
  renderWithMock("/register", mock)
  // Scoped to <main>: the lazy Register page is what we wait for; the nav also has a "Log in" link.
  await within(screen.getByRole("main")).findByRole("link", { name: "Log in" })
  await userEvent.type(await screen.findByLabelText("Username"), "alice")
  await userEvent.type(screen.getByLabelText("Password"), "secret123")
  await userEvent.click(screen.getByRole("button", { name: "Register" }))
  expect(await screen.findByRole("heading", { name: "Log in" })).toBeInTheDocument()
  expect(await screen.findByRole("status", { name: "" })).toHaveTextContent(/account was created/i)
})

it("stays logged in and shows an error when logout fails", async () => {
  const mock = await aliceMock(true)
  failNext(mock, "logout")
  renderWithMock("/alice/hello", mock)
  await userEvent.click(await screen.findByRole("button", { name: "Log out" }))
  expect(await screen.findByText(/could not log out/i)).toBeInTheDocument()
  expect(await within(screen.getByRole("navigation", { name: "Main" })).findByText("alice")).toBeInTheDocument()
  expect(await screen.findByRole("heading", { name: "alice/hello" })).toBeInTheDocument()
})
