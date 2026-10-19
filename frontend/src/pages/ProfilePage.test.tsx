import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, it, vi } from "vitest"
import { failNext, holdNext, renderWithMock } from "@/test-mock"
import { MockApiService } from "@/services/MockApiService"

const PW = "password1"
/** A mock where alice owns `repos` and nobody is logged in. */
async function withRepos(repos: { name: string; private?: boolean }[]) {
  const mock = new MockApiService()
  await mock.register("alice", PW)
  await mock.login("alice", PW)
  for (const r of repos) await mock.createRepo(r)
  await mock.logout()
  return mock
}

it("says so when the user has no visible repositories", async () => {
  const mock = new MockApiService()
  await mock.register("alice", PW)
  renderWithMock("/alice", mock)
  expect(await screen.findByText("alice has no repositories you can see.")).toBeInTheDocument()
  expect(screen.queryByRole("status", { name: "Loading repositories" })).not.toBeInTheDocument()
})

it.each([
  { who: "alice (the owner)", as: "alice", shown: true },
  { who: "another user", as: "bob", shown: false },
  { who: "a logged-out visitor", as: null, shown: false },
])("Create a repository link for $who: $shown", async ({ as, shown }) => {
  const mock = await withRepos([{ name: "hello" }])
  await mock.register("bob", PW)
  if (as) await mock.login(as, PW)
  else await mock.logout()
  renderWithMock("/alice", mock)
  await screen.findByRole("link", { name: "hello" })
  const link = screen.queryByRole("link", { name: "Create a repository" })
  if (shown) expect(link).toHaveAttribute("href", "/")
  else expect(link).not.toBeInTheDocument()
})

it("badges private repos as Private and public ones as Public", async () => {
  const mock = await withRepos([{ name: "open" }, { name: "hidden", private: true }])
  await mock.login("alice", PW) // the owner sees both
  renderWithMock("/alice", mock)
  const list = within(await screen.findByRole("list", { name: "Repository list" }))
  const row = (name: string) => list.getByRole("link", { name }).closest("li") as HTMLElement
  expect(within(row("open")).getByText("Public")).toBeInTheDocument()
  expect(within(row("hidden")).getByText("Private")).toBeInTheDocument()
})

// "a b" is not a valid username, so the instance is shaped to answer a page for it (the test is about link encoding, not the service).
it("encodes the owner in the pagination links", async () => {
  const mock = await withRepos([{ name: "x" }])
  const page = await mock.listRepos({ owner: "alice" })
  vi.spyOn(mock, "listRepos").mockResolvedValue({ ...page, page: 2, has_more: true })
  renderWithMock("/a%20b?page=2", mock)
  const nav = await screen.findByRole("navigation", { name: "Pagination" })
  expect(within(nav).getByRole("link", { name: "Next" })).toHaveAttribute("href", "/a%20b?page=3")
  expect(within(nav).getByRole("link", { name: "Previous" })).toHaveAttribute("href", "/a%20b")
})

it("shows a loading state while the list is pending", async () => {
  const mock = await withRepos([])
  const release = holdNext(mock, "listRepos")
  renderWithMock("/alice", mock)
  expect(await screen.findByRole("status", { name: "Loading repositories" })).toBeInTheDocument()
  release()
  expect(await screen.findByText("alice has no repositories you can see.")).toBeInTheDocument()
})

it("shows the error block and Retry re-requests the same page", async () => {
  const mock = await withRepos([{ name: "hello" }])
  failNext(mock, "listRepos", 500, "boom")
  const list = vi.spyOn(mock, "listRepos")
  renderWithMock("/alice", mock)
  const alert = await screen.findByRole("alert")
  expect(alert).toHaveTextContent("boom")
  await userEvent.click(within(alert).getByRole("button", { name: "Retry" }))
  expect(await screen.findByRole("link", { name: "hello" })).toHaveAttribute("href", "/alice/hello")
  expect(list).toHaveBeenCalledTimes(2)
  expect(list.mock.calls[1][0]).toMatchObject({ owner: "alice", page: 1 })
  expect(screen.queryByRole("alert")).not.toBeInTheDocument()
})
