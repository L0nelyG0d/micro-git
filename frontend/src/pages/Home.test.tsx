import { failGet, holdGet, holdNext, installMock, networkErrorGet, networkErrorNext, renderApp, renderWithMock } from "@/test-mock"
import { MockApiService } from "@/services/MockApiService"
import { ApiError } from "@/services/http"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, it, vi } from "vitest"
import { formatDate } from "@/lib/format"

/** A mock where alice owns `repos`; logged in as her or logged out. */
async function homeMock(loggedIn: boolean, repos: { name: string; private?: boolean }[] = []) {
  const mock = new MockApiService()
  await mock.register("alice", "secret123")
  await mock.login("alice", "secret123")
  for (const r of repos) await mock.createRepo(r)
  if (!loggedIn) await mock.logout()
  return mock
}

async function openForm() {
  await userEvent.click(await screen.findByRole("button", { name: "New repository" }))
}

it("shows the empty state pointing at login when logged out", async () => {
  renderWithMock("/", await homeMock(false))
  expect(await screen.findByText(/no repositories yet/i)).toBeInTheDocument()
  const main = screen.getByRole("main")
  expect(within(main).getAllByRole("link", { name: /log in/i }).length).toBeGreaterThan(0)
  expect(screen.queryByRole("button", { name: "New repository" })).not.toBeInTheDocument()
})

it("shows the empty state pointing at New repository when logged in", async () => {
  renderWithMock("/", await homeMock(true))
  expect(await screen.findByText(/no repositories yet.*new repository/i)).toBeInTheDocument()
  expect(await screen.findByRole("button", { name: "New repository" })).toBeInTheDocument()
})

it("creates a repo: toast, form closed, list updated without reload", async () => {
  const mock = await homeMock(true, [{ name: "old" }])
  renderWithMock("/", mock)
  await openForm()
  await userEvent.type(screen.getByLabelText("Name"), "new-one")
  await userEvent.type(screen.getByLabelText(/Description/), "Hi")
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  expect(await screen.findByText(/alice\/new-one created/i)).toBeInTheDocument()
  expect(screen.queryByRole("form", { name: "New repository" })).not.toBeInTheDocument()
  const links = screen.getAllByRole("link", { name: /^alice\// })
  expect(links.map((l) => l.textContent).sort()).toEqual(["alice/new-one", "alice/old"])
  expect(await mock.getRepo("alice", "new-one")).toMatchObject({ description: "Hi", private: false })
  // reopening gives a fresh form
  await openForm()
  expect(screen.getByLabelText("Name")).toHaveValue("")
})

it("validates client-side without calling the server", async () => {
  const mock = await homeMock(true)
  const create = vi.spyOn(mock, "createRepo")
  renderWithMock("/", mock)
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
  expect(create).not.toHaveBeenCalled()
})

it("shows 409 inline exactly as returned and keeps the input", async () => {
  renderWithMock("/", await homeMock(true, [{ name: "hello" }]))
  await openForm()
  await userEvent.type(screen.getByLabelText("Name"), "hello")
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  expect(await screen.findByRole("alert")).toHaveTextContent("repository already exists")
  expect(screen.getByLabelText("Name")).toHaveValue("hello")
  expect(screen.getByRole("button", { name: "Create repository" })).toBeEnabled()
})

it("on 401 shows a message and the nav goes to the logged-out state", async () => {
  const mock = await homeMock(true)
  renderWithMock("/", mock)
  await openForm()
  await userEvent.type(screen.getByLabelText("Name"), "hello")
  await mock.logout() // the session ends behind the page's back
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  // Inline message and toast both appear.
  expect((await screen.findAllByText(/session has expired/i)).length).toBeGreaterThanOrEqual(1)
  expect(await screen.findByRole("link", { name: "Register" })).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Log out" })).not.toBeInTheDocument()
})

it("refetches the list when the user logs out", async () => {
  renderWithMock("/", await homeMock(true, [{ name: "secret", private: true }, { name: "public" }]))
  await screen.findByRole("link", { name: "alice/secret" })
  await userEvent.click(screen.getByRole("button", { name: /log ?out/i }))
  await waitFor(() => expect(screen.queryByRole("link", { name: "alice/secret" })).not.toBeInTheDocument(), { timeout: 5000 })
  expect(screen.getByRole("link", { name: "alice/public" })).toBeInTheDocument()
})

const many = (n: number) => Array.from({ length: n }, (_, i) => ({ name: `r${i}` }))
const repoLinks = () => screen.queryAllByRole("link", { name: /^alice\// })

it("mock: page 1 with more shows a disabled Previous and a Next linking to page 2", async () => {
  renderWithMock("/", await homeMock(false, many(51)))
  const nav = await screen.findByRole("navigation", { name: "Pagination" })
  expect(within(nav).getByRole("button", { name: "Previous" })).toBeDisabled()
  expect(within(nav).getByText("Page 1")).toBeInTheDocument()
  expect(within(nav).getByRole("link", { name: "Next" })).toHaveAttribute("href", "/?page=2")
  expect(repoLinks()).toHaveLength(50)
})

it("mock: a single page shows no pagination controls", async () => {
  renderWithMock("/", await homeMock(false, many(3)))
  await screen.findByRole("link", { name: "alice/r0" })
  expect(screen.queryByRole("navigation", { name: "Pagination" })).not.toBeInTheDocument()
})

it("mock: the last page has Next disabled and Previous back to the first page", async () => {
  renderWithMock("/?page=2", await homeMock(false, many(51)))
  const nav = await screen.findByRole("navigation", { name: "Pagination" })
  expect(within(nav).getByRole("link", { name: "Previous" })).toHaveAttribute("href", "/")
  expect(within(nav).getByRole("button", { name: "Next" })).toBeDisabled()
  expect(within(nav).getByText("Page 2")).toBeInTheDocument()
  expect(repoLinks()).toHaveLength(1)
})

it("mock: a middle page links Previous to page 1 and Next to page 3", async () => {
  renderWithMock("/?page=2", await homeMock(false, many(101)))
  const nav = await screen.findByRole("navigation", { name: "Pagination" })
  expect(within(nav).getByRole("link", { name: "Previous" })).toHaveAttribute("href", "/")
  expect(within(nav).getByRole("link", { name: "Next" })).toHaveAttribute("href", "/?page=3")
})

it("mock: page 3 links Previous to page 2", async () => {
  renderWithMock("/?page=3", await homeMock(false, many(101)))
  const nav = await screen.findByRole("navigation", { name: "Pagination" })
  expect(within(nav).getByRole("link", { name: "Previous" })).toHaveAttribute("href", "/?page=2")
})

it("mock: an empty page past the end links to the first page", async () => {
  renderWithMock("/?page=9", await homeMock(false, many(3)))
  const link = await screen.findByRole("link", { name: "Go to the first page" })
  expect(link).toHaveAttribute("href", "/")
  expect(screen.queryByText("No repositories yet.", { exact: false })).not.toBeInTheDocument()
})

it("mock: an invalid ?page= falls back to page 1", async () => {
  renderWithMock("/?page=abc", await homeMock(false, many(51)))
  expect(await screen.findByRole("link", { name: "alice/r50" })).toBeInTheDocument()
  expect(screen.getByText("Page 1")).toBeInTheDocument()
})

it("mock: creating on a full page 1 keeps the page size and enables Next", async () => {
  renderWithMock("/", await homeMock(true, many(50)))
  await screen.findByRole("link", { name: "alice/r0" })
  await openForm()
  await userEvent.type(screen.getByLabelText("Name"), "fresh")
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  expect((await screen.findAllByText(/alice\/fresh created/i)).length).toBeGreaterThan(0)
  expect(repoLinks()).toHaveLength(50)
  expect(screen.getByRole("link", { name: "alice/fresh" })).toBeInTheDocument()
  expect(screen.getByRole("link", { name: "Next" })).toHaveAttribute("href", "/?page=2")
})

it("mock: moves focus to the Name input when the New repository form opens", async () => {
  renderWithMock("/", await homeMock(true))
  await openForm()
  expect(screen.getByLabelText("Name")).toHaveFocus()
  expect(screen.getByLabelText("Name")).toHaveAttribute("name", "name")
  expect(screen.getByLabelText("Name")).toHaveAttribute("spellcheck", "false")
})

it("mock: focuses the invalid Name after a failed submit and links it to the error", async () => {
  renderWithMock("/", await homeMock(true))
  await openForm()
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  const alert = await screen.findByRole("alert")
  const name = screen.getByLabelText("Name")
  expect(name).toHaveFocus()
  expect(name).toHaveAttribute("aria-invalid", "true")
  expect(name).toHaveAttribute("aria-describedby", alert.id)
})

it("mock: focuses the Description when only it is invalid", async () => {
  renderWithMock("/", await homeMock(true))
  await openForm()
  await userEvent.type(screen.getByLabelText("Name"), "ok")
  await userEvent.click(screen.getByLabelText(/Description/))
  await userEvent.paste("x".repeat(501))
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  expect(await screen.findByLabelText(/Description/)).toHaveFocus()
})

it("mock: focuses Name after the server rejects it with 409", async () => {
  renderWithMock("/", await homeMock(true, [{ name: "hello" }]))
  await openForm()
  await userEvent.type(screen.getByLabelText("Name"), "hello")
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  await screen.findByRole("alert")
  expect(screen.getByLabelText("Name")).toHaveFocus()
})

it("lists repos in the service's order with links, description and formatted date", async () => {
  const mock = await homeMock(false, [{ name: "first" }, { name: "second" }])
  await mock.register("bob", "secret123")
  await mock.login("bob", "secret123")
  await mock.createRepo({ name: "third", description: "A greeting" })
  await mock.logout()
  const served = (await mock.listRepos()).repos
  renderWithMock("/", mock)
  const links = await screen.findAllByRole("link", { name: /^(alice|bob)\// })
  expect(links.map((l) => l.textContent)).toEqual(served.map((r) => `${r.owner}/${r.name}`))
  expect(links[0]).toHaveAttribute("href", `/${served[0].owner}/${served[0].name}`)
  expect(screen.getByText("A greeting")).toBeInTheDocument()
  expect(screen.getAllByText(formatDate(served[0].created_at)).length).toBeGreaterThan(0)
  expect(formatDate(served[0].created_at)).toMatch(/^\d{2}\.\d{2}\.\d{4} \d{2}:\d{2}$/)
  expect(document.body).not.toHaveTextContent(/null|undefined/)
})

it("shows a loading state while the request is pending", async () => {
  const mock = await homeMock(false, [{ name: "hello" }])
  const release = holdGet(mock, "/repos?page=1")
  renderWithMock("/", mock)
  expect(await screen.findByRole("status", { name: "Loading repositories" })).toBeInTheDocument()
  release()
  expect(await screen.findByRole("link", { name: "alice/hello" })).toBeInTheDocument()
})

it("shows an error with Retry for a 500 and reloads on retry", async () => {
  const mock = await homeMock(false, [{ name: "hello" }])
  failGet(mock, "/repos?page=1")
  renderWithMock("/", mock)
  expect(await screen.findByRole("alert")).toHaveTextContent("internal error")
  await userEvent.click(screen.getByRole("button", { name: "Retry" }))
  expect(await screen.findByRole("link", { name: "alice/hello" })).toBeInTheDocument()
  expect(screen.queryByRole("alert")).not.toBeInTheDocument()
})

it("shows an error with Retry for a network error", async () => {
  const mock = await homeMock(false, [{ name: "hello" }])
  networkErrorGet(mock, "/repos?page=1")
  renderWithMock("/", mock)
  expect(await screen.findByRole("alert")).toHaveTextContent(/could not reach the server/i)
  expect(await screen.findByRole("button", { name: "Retry" })).toBeInTheDocument()
})

it("shows the unreachable block and the session indication when the backend is down, and Retry recovers", async () => {
  const mock = await homeMock(false, [{ name: "hello" }])
  networkErrorNext(mock, "me")
  networkErrorGet(mock, "/repos?page=1")
  renderWithMock("/", mock)
  expect(await screen.findByText("Session unavailable")).toBeInTheDocument()
  const block = await screen.findByRole("alert")
  expect(block).toHaveTextContent("Could not reach the server. Check your connection and try again.")
  await userEvent.click(within(block).getByRole("button", { name: "Retry" }))
  expect(await screen.findByRole("link", { name: "alice/hello" })).toBeInTheDocument()
  expect(screen.queryByText(/could not reach the server/i)).not.toBeInTheDocument()
})

it("shows server 400 messages inline", async () => {
  const mock = await homeMock(true)
  const create = vi.spyOn(mock, "createRepo")
  create.mockRejectedValueOnce(new ApiError(400, "invalid repository name"))
  renderWithMock("/", mock)
  await openForm()
  await userEvent.type(screen.getByLabelText("Name"), "hello")
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  expect(await screen.findByRole("alert")).toHaveTextContent("invalid repository name")
  create.mockRejectedValueOnce(new ApiError(400, "description must be at most 500 characters"))
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  expect(await screen.findByRole("alert")).toHaveTextContent("description must be at most 500 characters")
})

it("disables submit in flight and discards input on cancel", async () => {
  const mock = await homeMock(true)
  holdNext(mock, "createRepo")
  renderWithMock("/", mock)
  await openForm()
  await userEvent.type(screen.getByLabelText("Name"), "hello")
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  expect(screen.getByRole("button", { name: "Create repository" })).toBeDisabled()
  await userEvent.click(screen.getByRole("button", { name: "Cancel" }))
  await openForm()
  expect(screen.getByLabelText("Name")).toHaveValue("")
})

it("shows the cached list at once on a revisit while it revalidates", async () => {
  const mock = installMock(await homeMock(false, [{ name: "hello" }]))
  const first = renderApp("/")
  await screen.findByRole("link", { name: "alice/hello" })
  first.unmount()
  holdGet(mock, "/repos?page=1")
  renderApp("/")
  expect(await screen.findByRole("link", { name: "alice/hello" })).toBeInTheDocument()
  expect(screen.queryByRole("status", { name: "Loading repositories" })).not.toBeInTheDocument()
})

it("does not show a stale list on a revisit after creating a repo", async () => {
  installMock(await homeMock(true, [{ name: "hello" }]))
  const first = renderApp("/")
  await openForm()
  await userEvent.type(screen.getByLabelText("Name"), "fresh")
  await userEvent.click(screen.getByRole("button", { name: "Create repository" }))
  await screen.findAllByText(/alice\/fresh created/i)
  first.unmount()
  renderApp("/")
  expect(await screen.findByRole("link", { name: "alice/fresh" })).toBeInTheDocument()
  expect(screen.getAllByRole("link", { name: /^alice\// })).toHaveLength(2)
})
