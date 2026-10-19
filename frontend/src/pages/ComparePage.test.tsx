import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useLocation } from "react-router-dom"
import { expect, it } from "vitest"
import { failNext, holdNext, renderWithMock } from "@/test-mock"
import { MockApiService } from "@/services/MockApiService"

function Loc() {
  const l = useLocation()
  return <div data-testid="loc">{l.pathname + l.search}</div>
}
/** Logged-out visitor on a mock: alice/hello has main (a.txt = "old") and dev.1 (a.txt = "new"); `empty` leaves it without commits. */
async function withBranches(empty = false) {
  const mock = new MockApiService()
  await mock.register("alice", "password1")
  await mock.login("alice", "password1")
  await mock.createRepo({ name: "hello" })
  if (!empty) {
    await mock.createCommit("alice", "hello", {
      branch: "main", expected_tip: "", message: "first", changes: [{ path: "a.txt", content: "old\n" }],
    })
    await mock.createBranch("alice", "hello", { name: "dev.1", from: "main" })
    const tip = (await mock.listBranches("alice", "hello")).branches.find((b) => b.name === "dev.1")!.oid
    await mock.createCommit("alice", "hello", {
      branch: "dev.1", expected_tip: tip, message: "edit", changes: [{ path: "a.txt", content: "new\n" }],
    })
  }
  await mock.logout()
  return mock
}
const main = () => within(screen.getByRole("main"))

it("shows summary, diff lines and prefilled selects", async () => {
  renderWithMock("/alice/hello/compare?base=main&head=dev.1", await withBranches(), <Loc />)
  expect(await screen.findByTestId("summary")).toHaveTextContent("1 file changed, +1 -1")
  expect(screen.getByRole("combobox", { name: "Base" })).toHaveTextContent("main")
  expect(screen.getByRole("combobox", { name: "Compare" })).toHaveTextContent("dev.1")
  expect(screen.getByTestId("line-add")).toHaveTextContent("+new")
  expect(screen.getByTestId("line-del")).toHaveTextContent("-old")
})

it("shows the empty state for identical tips", async () => {
  renderWithMock("/alice/hello/compare?base=main&head=main", await withBranches())
  expect(await screen.findByText("No differences between these branches")).toBeInTheDocument()
  expect(screen.queryByTestId("summary")).not.toBeInTheDocument()
})

it("changing a select updates the URL and reloads", async () => {
  renderWithMock("/alice/hello/compare?base=main&head=dev.1", await withBranches(), <Loc />)
  expect(await screen.findByTestId("summary")).toHaveTextContent("1 file changed")
  await userEvent.click(await screen.findByRole("combobox", { name: "Base" }))
  await userEvent.click(screen.getByRole("option", { name: "dev.1" }))
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/compare?base=dev.1&head=dev.1")
  expect(await screen.findByText("No differences between these branches")).toBeInTheDocument()
})

it("shows the empty-repo hint", async () => {
  renderWithMock("/alice/hello/compare", await withBranches(true))
  expect(await screen.findByText(/This repository is empty/)).toBeInTheDocument()
})

it("repo page links to compare", async () => {
  renderWithMock("/alice/hello", await withBranches())
  expect(await screen.findByRole("link", { name: "Compare" })).toHaveAttribute("href", "/alice/hello/compare")
})

it("shows a loading state while the repository loads", async () => {
  const mock = await withBranches()
  const release = holdNext(mock, "getRepo")
  renderWithMock("/alice/hello/compare?base=main&head=dev.1", mock)
  expect(await main().findByRole("status", { name: "Loading repository" })).toBeInTheDocument()
  release()
  expect(await screen.findByTestId("summary")).toBeInTheDocument()
  expect(screen.getByRole("link", { name: "alice/hello" })).toHaveClass("max-sm:break-all")
})

it("a file over the diff limit shows the truncated note next to the normal diff", async () => {
  const mock = await withBranches()
  await mock.login("alice", "password1")
  const tip = (await mock.listBranches("alice", "hello")).branches.find((b) => b.name === "dev.1")!.oid
  await mock.createCommit("alice", "hello", {
    branch: "dev.1", expected_tip: tip, message: "big", changes: [{ path: "big.txt", content: "x\n".repeat(5001) }],
  })
  renderWithMock("/alice/hello/compare?base=main&head=dev.1", mock)
  expect(await screen.findByTestId("summary")).toHaveTextContent("2 files changed")
  expect(screen.getByText("diff too large to display")).toBeInTheDocument()
  expect(screen.getByTestId("line-add")).toHaveTextContent("+new")
})

it("shows an error with retry", async () => {
  const mock = await withBranches()
  failNext(mock, "compare", 500, "boom")
  renderWithMock("/alice/hello/compare?base=main&head=dev.1", mock)
  expect(await screen.findByRole("alert")).toHaveTextContent("boom")
  await userEvent.click(screen.getByRole("button", { name: "Retry" }))
  expect(await screen.findByTestId("summary")).toBeInTheDocument()
})
