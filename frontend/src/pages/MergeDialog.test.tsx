import { renderWithMock } from "@/test-mock"
import { MockApiService } from "@/services/MockApiService"
import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useLocation } from "react-router-dom"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import { toast } from "sonner"

beforeEach(() => {
  toastSpy = vi.spyOn(toast, "error").mockImplementation(() => "")
})
afterEach(() => toastSpy.mockRestore())

type Shape = "dev-ahead" | "main-ahead" | "diverged" | "same" | "single"
let toastSpy: ReturnType<typeof vi.spyOn>

/** alice/hello with main and (except "single") dev; `as` is who is logged in. */
async function merging(shape: Shape, as: string | null = "alice") {
  const mock = new MockApiService()
  await mock.register("alice", "password1")
  await mock.register("bob", "password1")
  await mock.login("alice", "password1")
  await mock.createRepo({ name: "hello" })
  const commit = async (branch: string, file: string) => {
    const tip = (await mock.listBranches("alice", "hello")).branches.find((b) => b.name === branch)?.oid ?? ""
    await mock.createCommit("alice", "hello", {
      branch, expected_tip: tip, message: `add ${file}`, changes: [{ path: file, content: file }],
    })
  }
  await commit("main", "a.txt")
  if (shape !== "single") await mock.createBranch("alice", "hello", { name: "dev", from: "main" })
  if (shape === "dev-ahead" || shape === "diverged") await commit("dev", "d.txt")
  if (shape === "main-ahead" || shape === "diverged") await commit("main", "m.txt")
  await mock.logout()
  if (as) await mock.login(as, "password1")
  return mock
}
const tipOf = async (mock: MockApiService, branch: string) =>
  (await mock.listBranches("alice", "hello")).branches.find((b) => b.name === branch)!.oid

function Loc() {
  return <div data-testid="loc">{useLocation().pathname}</div>
}
async function openDialog() {
  await userEvent.click(await screen.findByRole("button", { name: "Merge" }))
  return screen.getByRole("alertdialog")
}

it("owner with two branches sees Merge; defaults into=main, from=dev", async () => {
  renderWithMock("/alice/hello", await merging("dev-ahead"), <Loc />)
  const dlg = await openDialog()
  expect(within(dlg).getByRole("combobox", { name: "Into" })).toHaveTextContent("main")
  expect(within(dlg).getByRole("combobox", { name: "Merge from" })).toHaveTextContent("dev")
  expect(within(dlg).getByText("Fast-forward only. main will point to the tip of dev.")).toBeInTheDocument()
  expect(within(dlg).getByRole("button", { name: "Merge" })).toBeEnabled()
})

it("owner repo page shows Compare, Merge and Settings together", async () => {
  renderWithMock("/alice/hello", await merging("dev-ahead"))
  expect(await screen.findByRole("link", { name: "Compare" })).toBeInTheDocument()
  expect(await screen.findByRole("button", { name: "Merge" })).toBeInTheDocument()
  expect(await screen.findByRole("link", { name: "Settings" })).toBeInTheDocument()
})

it("is hidden from non-owners", async () => {
  renderWithMock("/alice/hello", await merging("dev-ahead", "bob"))
  await screen.findByRole("table", { name: "Files" })
  expect(screen.queryByRole("button", { name: "Merge" })).not.toBeInTheDocument()
})

it("is hidden on a single-branch repo", async () => {
  renderWithMock("/alice/hello", await merging("single"))
  await screen.findByRole("table", { name: "Files" })
  expect(screen.queryByRole("button", { name: "Merge" })).not.toBeInTheDocument()
})

it("submit is disabled while both selects are the same branch", async () => {
  renderWithMock("/alice/hello", await merging("dev-ahead"))
  const dlg = await openDialog()
  await userEvent.click(within(dlg).getByRole("combobox", { name: "Merge from" }))
  await userEvent.click(screen.getByRole("option", { name: "main (default)" }))
  expect(within(dlg).getByRole("button", { name: "Merge" })).toBeDisabled()
})

it("success: merges, toasts, closes and shows the merged branch's files", async () => {
  const mock = await merging("dev-ahead")
  renderWithMock("/alice/hello", mock)
  expect(await screen.findByRole("link", { name: "a.txt" })).toBeInTheDocument()
  expect(screen.queryByRole("link", { name: "d.txt" })).not.toBeInTheDocument()
  const dlg = await openDialog()
  await userEvent.click(within(dlg).getByRole("button", { name: "Merge" }))
  await vi.waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
  expect(await screen.findByText("main now points to the tip of dev.")).toBeInTheDocument()
  expect(await tipOf(mock, "main")).toBe(await tipOf(mock, "dev"))
  // The page reloaded the target branch: dev's file is listed now.
  expect(await screen.findByRole("link", { name: "d.txt" })).toBeInTheDocument()
})

it("success with a different target navigates to it", async () => {
  const mock = await merging("main-ahead")
  renderWithMock("/alice/hello", mock, <Loc />)
  const dlg = await openDialog()
  await userEvent.click(within(dlg).getByRole("combobox", { name: "Into" }))
  await userEvent.click(screen.getByRole("option", { name: "dev" }))
  await userEvent.click(within(dlg).getByRole("combobox", { name: "Merge from" }))
  await userEvent.click(screen.getByRole("option", { name: "main (default)" }))
  await userEvent.click(within(dlg).getByRole("button", { name: "Merge" }))
  await vi.waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/tree/dev"))
  expect(await tipOf(mock, "dev")).toBe(await tipOf(mock, "main"))
})

it("409 not a fast-forward shows the message and advice, dialog stays open", async () => {
  renderWithMock("/alice/hello", await merging("diverged"))
  const dlg = await openDialog()
  await userEvent.click(within(dlg).getByRole("button", { name: "Merge" }))
  const alert = await within(dlg).findByRole("alert")
  expect(alert).toHaveTextContent("not a fast-forward")
  expect(alert).toHaveTextContent(/create a new branch or commit on top of the target/i)
  expect(within(dlg).getByRole("button", { name: "Merge" })).toBeEnabled()
})

it("400 already up to date is shown inline", async () => {
  renderWithMock("/alice/hello", await merging("same"))
  const dlg = await openDialog()
  await userEvent.click(within(dlg).getByRole("button", { name: "Merge" }))
  expect(await within(dlg).findByRole("alert")).toHaveTextContent("already up to date")
})

const EXPIRED = "Your session has expired. Log in again."

it("401 expires the session with a toast and hides owner actions", async () => {
  const mock = await merging("dev-ahead")
  renderWithMock("/alice/hello", mock, <Loc />)
  const dlg = await openDialog()
  await mock.logout() // the session ends behind the page's back
  await userEvent.click(within(dlg).getByRole("button", { name: "Merge" }))
  await vi.waitFor(() => expect(toastSpy).toHaveBeenCalledWith(EXPIRED))
  expect(screen.getByRole("link", { name: "Log in" })).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Merge" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Delete repository" })).not.toBeInTheDocument()
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello")
})

it("409 branch tip has moved offers Reload, which reloads the page data", async () => {
  const mock = await merging("dev-ahead")
  const c1 = await tipOf(mock, "main")
  renderWithMock("/alice/hello", mock)
  const dlg = await openDialog()
  // Another tab moves main between the mock's checks and its compare-and-swap.
  mock.beforeMergeSwap = async () => {
    await mock.createCommit("alice", "hello", { branch: "main", expected_tip: c1, message: "race", changes: [{ path: "r.txt", content: "r" }] })
    mock.beforeMergeSwap = undefined
  }
  await userEvent.click(within(dlg).getByRole("button", { name: "Merge" }))
  const alert = await within(dlg).findByRole("alert")
  expect(alert).toHaveTextContent("branch tip has moved")
  const loads = vi.spyOn(mock, "listBranches")
  await userEvent.click(within(alert).getByRole("button", { name: "Reload" }))
  await vi.waitFor(() => expect(loads).toHaveBeenCalled())
  expect(await tipOf(mock, "main")).not.toBe(await tipOf(mock, "dev"))
})
it("long branch names are clipped inside the dialog and keep the full name as a title", async () => {
  const long = "feature/" + "x".repeat(24) // 32 characters; "/" is not a valid branch character, so use dots
  const name = long.replace("/", ".")
  const mock = await merging("single")
  await mock.logout()
  await mock.login("alice", "password1")
  await mock.createBranch("alice", "hello", { name, from: "main" })
  renderWithMock("/alice/hello", mock)
  const dlg = await openDialog()
  // jsdom has no layout; the 320px result is checked in a real browser. Here:
  // the classes that stop the select from widening the dialog grid.
  expect(dlg.className).toContain("grid-cols-[minmax(0,1fr)]")
  const from = within(dlg).getByRole("combobox", { name: "Merge from" })
  expect(from).toHaveAttribute("title", name)
  expect(from.className).toContain("min-w-0")
  expect(within(from).getByText(name).className).toContain("truncate")
})

it("dialog title and description wrap long unbroken strings", async () => {
  renderWithMock("/alice/hello", await merging("dev-ahead"))
  const dlg = await openDialog()
  for (const el of [within(dlg).getByText("Merge branches"), within(dlg).getByText(/^Fast-forward only/)]) {
    expect(el.className).toContain("min-w-0")
    expect(el.className).toContain("[overflow-wrap:anywhere]")
  }
})
