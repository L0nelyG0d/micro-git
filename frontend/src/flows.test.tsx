// Screen flows on top of MockApiService (#102): no fetch stub. renderWithMock()
// swaps the service; transport behaviour (network failure, 429, 5xx without JSON)
// stays in the fetch-stub tests. Mock-only values (oids, diff text) are not asserted.
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { toast } from "sonner"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import { renderWithMock } from "@/test-mock"
import { MockApiService } from "@/services/MockApiService"

const PW = "password1"
let toastSpy: ReturnType<typeof vi.spyOn>
beforeEach(() => {
  toastSpy = vi.spyOn(toast, "error").mockImplementation(() => "")
})
afterEach(() => toastSpy.mockRestore())

/** Registers `name` on the mock, leaves it logged out (as the backend does). */
/** Fills a field in one step (typing char by char costs most of the flow time). */
async function fill(el: HTMLElement, text: string) {
  await userEvent.click(el)
  await userEvent.paste(text)
}

async function addUser(mock: MockApiService, name: string) {
  await mock.register(name, PW)
}
async function seedRepo(mock: MockApiService, name: string, repo: string, opts: { private?: boolean } = {}) {
  await mock.login(name, PW)
  await mock.createRepo({ name: repo, private: opts.private })
  await mock.createCommit(name, repo, {
    branch: "main",
    expected_tip: "",
    message: "first",
    changes: [{ path: "README.md", content: "hello\n" }],
  })
}

it("register then log in", async () => {
  renderWithMock("/register")
  await fill(await screen.findByLabelText("Username"), "carol")
  await fill(screen.getByLabelText("Password"), PW)
  await userEvent.click(screen.getByRole("button", { name: "Register" }))
  // Register logs in straight away and lands on the home page.
  expect(await screen.findByRole("button", { name: "Log out" })).toBeInTheDocument()
  expect(screen.getByText("carol")).toBeInTheDocument()
  await userEvent.click(screen.getByRole("button", { name: "Log out" }))
  expect(await screen.findByRole("link", { name: "Log in" })).toBeInTheDocument()
  await userEvent.click(screen.getByRole("link", { name: "Log in" }))
  await fill(await screen.findByLabelText("Username"), "carol")
  await fill(screen.getByLabelText("Password"), PW)
  await userEvent.click(screen.getByRole("button", { name: "Log in" }))
  expect(await screen.findByRole("button", { name: "Log out" })).toBeInTheDocument()
})

it("create a repository", async () => {
  const { mock } = renderWithMock("/login")
  await addUser(mock, "ann")
  await fill(await screen.findByLabelText("Username"), "ann")
  await fill(screen.getByLabelText("Password"), PW)
  await userEvent.click(screen.getByRole("button", { name: "Log in" }))
  await userEvent.click(await screen.findByRole("button", { name: "New repository" }))
  await fill(screen.getByLabelText("Name"), "hello")
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  expect(await screen.findByRole("link", { name: /hello/ })).toBeInTheDocument()
  expect((await mock.listRepos({ owner: "ann" })).repos.map((r) => r.name)).toEqual(["hello"])
})

it("edit a file and commit: the commit shows in history with its diff", async () => {
  const mock = new MockApiService()
  await addUser(mock, "ann")
  await seedRepo(mock, "ann", "r") // leaves ann logged in
  renderWithMock("/ann/r/edit/main/README.md", mock)
  const content = await screen.findByLabelText("File content")
  await waitFor(() => expect(content).toHaveValue("hello\n"))
  await userEvent.clear(content)
  await fill(content, "changed")
  await fill(screen.getByLabelText("Commit message"), "update readme")
  await userEvent.click(screen.getByRole("button", { name: "Commit" }))
  // The editor lands on the new commit: message and the diff of the edit.
  expect(await screen.findByText("update readme")).toBeInTheDocument()
  expect(await screen.findByText("changed", { selector: "td, span, div, pre, code" })).toBeInTheDocument()
  // The history lists it too.
  await userEvent.click(await screen.findByRole("link", { name: /ann\/r/ }))
  await userEvent.click(await screen.findByRole("link", { name: /Commits/ }))
  expect(await screen.findByText("first")).toBeInTheDocument()
  expect(screen.getByText("update readme")).toBeInTheDocument()
})

it("create a branch and delete it", async () => {
  const mock = new MockApiService()
  await addUser(mock, "ann")
  await seedRepo(mock, "ann", "r")
  renderWithMock("/ann/r", mock)
  await userEvent.click(await screen.findByRole("button", { name: "Create branch" }))
  await fill(screen.getByLabelText("Branch name"), "feature")
  await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Create branch" }))
  await waitFor(async () =>
    expect((await mock.listBranches("ann", "r")).branches.map((b) => b.name)).toContain("feature"),
  )
  await userEvent.click(await screen.findByRole("button", { name: "Delete branch" }))
  await userEvent.click(await screen.findByRole("button", { name: "Delete feature" }))
  await userEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Delete branch" }))
  await waitFor(async () =>
    expect((await mock.listBranches("ann", "r")).branches.map((b) => b.name)).not.toContain("feature"),
  )
})

it("make a repository private: the owner still sees it, a second user gets 404", async () => {
  const mock = new MockApiService()
  await addUser(mock, "ann")
  await addUser(mock, "bob")
  await seedRepo(mock, "ann", "r")
  renderWithMock("/ann/r/settings", mock)
  await userEvent.click(await screen.findByRole("button", { name: "Change visibility" }))
  await userEvent.click(await screen.findByRole("button", { name: "Make private" }))
  await waitFor(async () => expect((await mock.getRepo("ann", "r")).private).toBe(true))
  // Bob logs in (new mock session) and opens the repository.
  await mock.logout()
  await mock.login("bob", PW)
  renderWithMock("/ann/r", mock)
  expect(await screen.findByText("Repository not found")).toBeInTheDocument()
})

it("session expiry: logout() on the same mock, then a write shows the toast and logs out the nav", async () => {
  const mock = new MockApiService()
  await addUser(mock, "ann")
  await seedRepo(mock, "ann", "r")
  renderWithMock("/ann/r/settings", mock)
  await userEvent.click(await screen.findByRole("button", { name: "Change visibility" }))
  await mock.logout() // the session ends behind the page's back
  await userEvent.click(await screen.findByRole("button", { name: "Make private" }))
  await waitFor(() => expect(toastSpy).toHaveBeenCalledWith("Your session has expired. Log in again."))
  const nav = within(screen.getByRole("navigation", { name: "Main" }))
  expect(await nav.findByRole("link", { name: "Log in" })).toBeInTheDocument()
  expect(nav.queryByRole("button", { name: "Log out" })).not.toBeInTheDocument()
})
