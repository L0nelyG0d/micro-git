import { failNext, renderWithMock } from "@/test-mock"
import { MockApiService } from "@/services/MockApiService"
import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useLocation } from "react-router-dom"
import { expect, it } from "vitest"

/** A mock where alice owns the empty repository "hello" and is logged in. */
async function seeded() {
  const mock = new MockApiService()
  await mock.register("alice", "password1")
  await mock.login("alice", "password1")
  await mock.createRepo({ name: "hello" })
  return mock
}

function Loc() {
  return <div data-testid="loc">{useLocation().pathname}</div>
}
async function openDialog() {
  await userEvent.click(await screen.findByRole("button", { name: "Rename" }))
  return screen.getByRole("dialog")
}

it("rename: prefilled field, success navigates to the new name with a toast", async () => {
  const mock = await seeded()
  renderWithMock("/alice/hello/settings", mock, <Loc />)
  const dlg = await openDialog()
  const input = within(dlg).getByLabelText("Repository name")
  expect(input).toHaveValue("hello")
  await userEvent.clear(input)
  await userEvent.type(input, "renamed")
  await userEvent.click(within(dlg).getByRole("button", { name: "Rename repository" }))
  await vi.waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/alice/renamed"))
  expect((await mock.getRepo("alice", "renamed")).name).toBe("renamed")
  expect(await screen.findByText(/renamed to alice\/renamed/i)).toBeInTheDocument()
  expect(await screen.findByRole("heading", { name: "alice/renamed" })).toBeInTheDocument()
})

it("rename: a taken name shows the duplicate message inline and stays", async () => {
  const mock = await seeded()
  await mock.createRepo({ name: "hello2" })
  renderWithMock("/alice/hello/settings", mock, <Loc />)
  const dlg = await openDialog()
  await userEvent.type(within(dlg).getByLabelText("Repository name"), "2")
  await userEvent.click(within(dlg).getByRole("button", { name: "Rename repository" }))
  expect(await within(dlg).findByRole("alert")).toHaveTextContent(
    "A repository with this name already exists. Choose another name.",
  )
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/settings")
})

it("rename: an invalid name is rejected before any request", async () => {
  const mock = await seeded()
  const update = vi.spyOn(mock, "updateRepo")
  renderWithMock("/alice/hello/settings", mock, <Loc />)
  const dlg = await openDialog()
  const input = within(dlg).getByLabelText("Repository name")
  await userEvent.clear(input)
  await userEvent.type(input, "a b")
  await userEvent.click(within(dlg).getByRole("button", { name: "Rename repository" }))
  expect(await within(dlg).findByRole("alert")).toHaveTextContent("letters, digits")
  expect(update).not.toHaveBeenCalled()
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/settings")
})

it.each([["bob"], [null]])("rename: button hidden for non-owner (%s)", async (u) => {
  const mock = await seeded()
  await mock.register("bob", "password1")
  if (u) await mock.login(u, "password1")
  else await mock.logout()
  renderWithMock("/alice/hello/settings", mock)
  await screen.findByRole("heading", { name: "alice/hello" })
  expect(screen.queryByRole("button", { name: "Rename" })).not.toBeInTheDocument()
})

it("old URL after a rename shows the repository not found state", async () => {
  const mock = await seeded()
  await mock.updateRepo("alice", "hello", { name: "renamed" })
  renderWithMock("/alice/hello/settings", mock)
  expect(await screen.findByText("Repository not found")).toBeInTheDocument()
})

const EXPIRED = "Your session has expired. Log in again."
function expectToast() {
  expect(document.querySelector("[data-sonner-toast]")).toHaveTextContent(EXPIRED)
}
function expectLoggedOut() {
  expectToast()
  expect(within(screen.getByRole("main")).getByRole("link", { name: "Log in" })).toBeInTheDocument()
}

it("rename: 401 expires the session with a toast and hides owner actions", async () => {
  const mock = await seeded()
  renderWithMock("/alice/hello/settings", mock, <Loc />)
  const dlg = await openDialog()
  await mock.logout() // the session ends behind the page's back
  await userEvent.type(within(dlg).getByLabelText("Repository name"), "2")
  await userEvent.click(within(dlg).getByRole("button", { name: "Rename repository" }))
  await vi.waitFor(() => expectToast())
  expectLoggedOut()
  expect(screen.queryByRole("button", { name: "Rename" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Delete repository" })).not.toBeInTheDocument()
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/settings")
})

it("rename: a 400 from the server shows the name rule", async () => {
  const mock = await seeded()
  failNext(mock, "updateRepo", 400, "invalid repository name")
  renderWithMock("/alice/hello/settings", mock, <Loc />)
  const dlg = await openDialog()
  const input = within(dlg).getByLabelText("Repository name")
  await userEvent.clear(input)
  await userEvent.type(input, "ok")
  await userEvent.click(within(dlg).getByRole("button", { name: "Rename repository" }))
  expect(await within(dlg).findByRole("alert")).toHaveTextContent("Use 1-100 letters, digits, dots, hyphens or underscores.")
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/settings")
})
