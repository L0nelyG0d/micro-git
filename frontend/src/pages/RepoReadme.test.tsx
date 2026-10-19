import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, it, vi } from "vitest"
import { failGet, holdGet, renderWithMock } from "@/test-mock"
import { MockApiService } from "@/services/MockApiService"

/** Logged-out visitor on a mock where alice has a repo "hello" with the given files on main. */
async function withFiles(files: Record<string, string>, devReadme?: string) {
  const mock = new MockApiService()
  await mock.register("alice", "password1")
  await mock.login("alice", "password1")
  await mock.createRepo({ name: "hello" })
  await mock.createCommit("alice", "hello", {
    branch: "main",
    expected_tip: "",
    message: "files",
    changes: Object.entries(files).map(([path, content]) => ({ path, content })),
  })
  if (devReadme !== undefined) {
    await mock.createBranch("alice", "hello", { name: "dev", from: "main" })
    const tip = (await mock.listBranches("alice", "hello")).branches.find((b) => b.name === "dev")!.oid
    await mock.createCommit("alice", "hello", {
      branch: "dev",
      expected_tip: tip,
      message: "dev readme",
      changes: [{ path: "README.md", content: devReadme }],
    })
  }
  await mock.logout()
  return mock
}

it("shows the README rendered below the file tree, markup as text", async () => {
  const mock = await withFiles({
    "README.md": "# Hello\n\nsome **bold** <script>alert(1)</script>\n",
    "src/a.txt": "a",
  })
  renderWithMock("/alice/hello", mock)
  const card = await screen.findByTestId("readme")
  expect(within(card).getByText("README.md")).toBeInTheDocument()
  expect(await within(card).findByRole("heading", { name: "Hello" })).toBeInTheDocument()
  expect(card).toHaveTextContent("<script>alert(1)</script>")
  expect(card.querySelector("script")).toBeNull()
  expect((await screen.findByRole("table", { name: "Files" })).compareDocumentPosition(card) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
})

it("has no card when the tree has no README.md", async () => {
  renderWithMock("/alice/hello", await withFiles({ "src/a.txt": "a" }))
  await screen.findByRole("table", { name: "Files" })
  expect(screen.queryByTestId("readme")).not.toBeInTheDocument()
})

it("has no card in a subdirectory", async () => {
  renderWithMock("/alice/hello/tree/main/src", await withFiles({ "README.md": "# Top", "src/README.md": "# Inner" }))
  await screen.findByRole("table", { name: "Files" })
  expect(screen.queryByTestId("readme")).not.toBeInTheDocument()
})

it("follows the ref: a branch route shows that branch's README", async () => {
  renderWithMock("/alice/hello/tree/dev", await withFiles({ "README.md": "main readme" }, "dev readme"))
  expect(await screen.findByText("dev readme")).toBeInTheDocument()
  expect(screen.queryByText("main readme")).not.toBeInTheDocument()
})

it("does not request the README when the tree lists none", async () => {
  const mock = await withFiles({ "src/a.txt": "a" })
  const blob = vi.spyOn(mock, "getBlob")
  renderWithMock("/alice/hello", mock)
  expect(await screen.findByRole("table", { name: "Files" })).toBeInTheDocument()
  expect(screen.queryByTestId("readme")).not.toBeInTheDocument()
  expect(blob).not.toHaveBeenCalled()
})

it("shows a skeleton while the README loads", async () => {
  const mock = await withFiles({ "README.md": "x" })
  const release = holdGet(mock, "/blob/main/README.md")
  renderWithMock("/alice/hello", mock)
  expect(await screen.findByRole("status", { name: "Loading README" })).toBeInTheDocument()
  release()
  expect(await screen.findByText("x")).toBeInTheDocument()
})

it("failed README fetch shows a note with retry and keeps the tree", async () => {
  const mock = await withFiles({ "README.md": "back" })
  failGet(mock, "/blob/main/README.md")
  renderWithMock("/alice/hello", mock)
  const card = await screen.findByTestId("readme")
  expect(await within(card).findByRole("alert")).toHaveTextContent("Could not load the README")
  expect(await screen.findByRole("table", { name: "Files" })).toBeInTheDocument()
  await userEvent.click(within(card).getByRole("button", { name: "Retry" }))
  expect(await within(card).findByText("back")).toBeInTheDocument()
})
