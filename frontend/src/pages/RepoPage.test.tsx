import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useLocation } from "react-router-dom"
import { expect, it, vi } from "vitest"
import { alterGet, failGet, failGetAfterHold, failNext, holdGet, readPaths, renderWithMock } from "@/test-mock"
import { MockApiService } from "@/services/MockApiService"

function Loc() {
  return <div data-testid="loc">{useLocation().pathname}</div>
}
const main = () => within(screen.getByRole("main"))

async function openCreate() {
  await userEvent.click(await screen.findByRole("button", { name: "Create branch" }))
  return screen.getByLabelText("Branch name")
}

const EXPIRED = "Your session has expired. Log in again."
function expectToast() {
  expect(document.querySelector("[data-sonner-toast]")).toHaveTextContent(EXPIRED)
}
// The page body may also render its own "Log in" link (settings), and it commits
// a render later than the nav, so scope to the nav and wait for the real condition.
async function expectLoggedOut() {
  await vi.waitFor(() => expectToast())
  expect(await within(screen.getByRole("navigation", { name: "Main" })).findByRole("link", { name: "Log in" })).toBeInTheDocument()
}

// ---- Screens on top of MockApiService (#102) ----

/** alice/hello: main has README.md, a.txt, src/main.go, src/lib/a.go; dev branches off main. `as` is who is logged in. */
async function hello(as: "alice" | "bob" | null, opts: { empty?: boolean } = {}) {
  const mock = new MockApiService()
  await mock.register("alice", "password1")
  await mock.register("bob", "password1")
  await mock.login("alice", "password1")
  await mock.createRepo({ name: "hello", description: "A greeting" })
  if (!opts.empty) {
    await mock.createCommit("alice", "hello", {
      branch: "main",
      expected_tip: "",
      message: "Add readme\n\nlong body",
      changes: [
        { path: "README.md", content: "# hello\n" },
        { path: "a.txt", content: "  <b>hi</b>\n\tindent  \n" },
        { path: "src/main.go", content: "package main\n" },
        { path: "src/lib/a.go", content: "package lib\n" },
        { path: "e", content: "" },
      ],
    })
    await mock.createBranch("alice", "hello", { name: "dev", from: "main" })
  }
  await mock.logout()
  if (as) await mock.login(as, "password1")
  return mock
}
const branchNames = async (mock: MockApiService) =>
  (await mock.listBranches("alice", "hello")).branches.map((b) => b.name).sort()
const mainTip = async (mock: MockApiService) =>
  (await mock.listBranches("alice", "hello")).branches.find((b) => b.name === "main")!.oid
const tick = () => new Promise((r) => setTimeout(r, 0))
const at = (path: string, mock: MockApiService) => renderWithMock(path, mock, <Loc />)

it("mock: shows the tree with links to directories and files", async () => {
  at("/alice/hello", await hello(null))
  const files = await screen.findByRole("table", { name: "Files" })
  const href = (name: string) => within(files).getByRole("link", { name }).getAttribute("href")
  expect(href("src")).toBe("/alice/hello/tree/main/src")
  expect(href("README.md")).toBe("/alice/hello/blob/main/README.md")
})

it("mock: shows the latest commit bar from the newest commit", async () => {
  const mock = await hello(null)
  const oid = (await mock.listCommits("alice", "hello", { ref: "main" })).commits[0].oid
  at("/alice/hello", mock)
  const link = await screen.findByRole("link", { name: oid.slice(0, 7) })
  expect(link).toHaveAttribute("href", `/alice/hello/commit/${oid}`)
  expect(main().getByText("Add readme")).toBeInTheDocument()
  expect(main().queryByText(/long body/)).not.toBeInTheDocument()
})

it("mock: empty repo: a visitor sees a plain message, the owner sees Add file", async () => {
  const visitor = at("/alice/hello", await hello(null, { empty: true }))
  expect(await screen.findByText(/repository is empty/i)).toBeInTheDocument()
  expect(screen.queryByRole("link", { name: "Add file" })).not.toBeInTheDocument()
  visitor.unmount()
  at("/alice/hello", await hello("alice", { empty: true }))
  expect(await screen.findByRole("link", { name: "Add file" })).toHaveAttribute("href", "/alice/hello/new/main")
})

it("mock: unknown repo shows not found", async () => {
  at("/alice/nope", await hello(null))
  expect(await screen.findByText("Repository not found")).toBeInTheDocument()
})

it("mock: directory view: breadcrumbs link back, files link to blobs", async () => {
  at("/alice/hello/tree/main/src/lib", await hello(null))
  const crumbs = await screen.findByRole("navigation", { name: "Breadcrumbs" })
  expect(within(crumbs).getAllByRole("link").map((l) => l.getAttribute("href"))).toEqual([
    "/alice/hello/tree/main",
    "/alice/hello/tree/main/src",
    "/alice/hello/tree/main/src/lib",
  ])
  expect(await screen.findByRole("link", { name: "a.go" })).toHaveAttribute("href", "/alice/hello/blob/main/src/lib/a.go")
})

it("mock: an unknown path shows a not-found state", async () => {
  at("/alice/hello/tree/main/nope", await hello(null))
  expect(await screen.findByTestId("not-found")).toHaveTextContent(/path not found/i)
})

it("mock: owner on a branch sees Add file, Create branch and Settings", async () => {
  at("/alice/hello", await hello("alice"))
  expect(await screen.findByRole("link", { name: "Add file" })).toHaveAttribute("href", "/alice/hello/new/main")
  expect(await screen.findByRole("button", { name: "Create branch" })).toBeInTheDocument()
  expect(await screen.findByRole("link", { name: "Settings" })).toHaveAttribute("href", "/alice/hello/settings")
})

it.each([["bob"], [null]] as const)("mock: non-owner (%s) sees no write actions", async (u) => {
  at("/alice/hello", await hello(u))
  await screen.findByRole("table", { name: "Files" })
  for (const n of ["Add file", "Edit", "Create branch", "Delete repository", "Delete branch"]) {
    expect(screen.queryByRole("link", { name: n })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: n })).not.toBeInTheDocument()
  }
})

it("mock: matches the owner case-insensitively", async () => {
  at("/Alice/hello", await hello("alice"))
  expect(await screen.findByRole("link", { name: "Settings" })).toBeInTheDocument()
})

it("mock: a commit oid as ref is read-only", async () => {
  const mock = await hello("alice")
  at(`/alice/hello/tree/${await mainTip(mock)}`, mock)
  await screen.findByRole("table", { name: "Files" })
  expect(screen.queryByRole("link", { name: "Add file" })).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "Create branch" })).not.toBeInTheDocument()
})

it("mock: branch switcher marks the default and switching changes the URL", async () => {
  at("/alice/hello", await hello(null))
  await userEvent.click(await screen.findByRole("combobox", { name: "Branch" }))
  expect(screen.getByRole("option", { name: "main (default)" })).toBeInTheDocument()
  await userEvent.click(screen.getByRole("option", { name: "dev" }))
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/tree/dev")
  await screen.findByRole("table", { name: "Files" })
})

it("mock: create branch starts from the current branch and switches to it", async () => {
  const mock = await hello("alice")
  at("/alice/hello", mock)
  await userEvent.type(await openCreate(), "feature")
  await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Create branch" }))
  await vi.waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/tree/feature"))
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
  const list = (await mock.listBranches("alice", "hello")).branches
  expect(list.find((b) => b.name === "feature")!.oid).toBe(list.find((b) => b.name === "main")!.oid)
})

it("mock: create branch 409 for an existing name is shown inline in the dialog", async () => {
  at("/alice/hello", await hello("alice"))
  await userEvent.type(await openCreate(), "dev")
  await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Create branch" }))
  expect(await within(screen.getByRole("dialog")).findByRole("alert")).toHaveTextContent("branch already exists")
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello")
})

it.each([["feature/x"], ["a b"], [".."], ["a".repeat(101)], ["a".repeat(40)], ["é"]])(
  "mock: create branch: invalid name %j shows an inline error and creates nothing",
  async (bad) => {
    const mock = await hello("alice")
    at("/alice/hello", mock)
    const input = await openCreate()
    await userEvent.type(input, bad)
    await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Create branch" }))
    const alert = within(screen.getByRole("dialog")).getByRole("alert")
    expect(alert).toHaveTextContent("invalid branch name")
    expect(input).toHaveFocus()
    expect(input).toHaveAttribute("aria-invalid", "true")
    expect(input).toHaveAccessibleDescription("invalid branch name")
    expect(await branchNames(mock)).toEqual(["dev", "main"])
  },
)

it("mock: create branch accepts boundary names (100 chars, dots, dashes)", async () => {
  const mock = await hello("alice")
  const name = "." + "a".repeat(98) + "-"
  at("/alice/hello", mock)
  await userEvent.click(await screen.findByRole("button", { name: "Create branch" }))
  await userEvent.click(screen.getByLabelText("Branch name"))
  await userEvent.paste(name)
  await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Create branch" }))
  await vi.waitFor(async () => expect(await branchNames(mock)).toContain(name))
})

it("mock: delete repo: dialog names the repo, confirm deletes and redirects with a toast", async () => {
  const mock = await hello("alice")
  at("/alice/hello/settings", mock)
  await userEvent.click(await screen.findByRole("button", { name: "Delete repository" }))
  const dlg = screen.getByRole("alertdialog")
  expect(dlg).toHaveTextContent("alice/hello")
  await userEvent.click(within(dlg).getByRole("button", { name: "Delete repository" }))
  await vi.waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent(/^\/$/))
  expect(await screen.findByText(/alice\/hello deleted/i)).toBeInTheDocument()
  await expect(mock.getRepo("alice", "hello")).rejects.toMatchObject({ status: 404 })
})

it("mock: delete repo: cancel keeps the repository", async () => {
  const mock = await hello("alice")
  at("/alice/hello/settings", mock)
  await userEvent.click(await screen.findByRole("button", { name: "Delete repository" }))
  await userEvent.click(screen.getByRole("button", { name: "Cancel" }))
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument()
  expect((await mock.getRepo("alice", "hello")).name).toBe("hello")
})

it("mock: delete branch: owner only, default branch excluded", async () => {
  at("/alice/hello", await hello("alice"))
  await userEvent.click(await screen.findByRole("button", { name: "Delete branch" }))
  const dlg = screen.getByRole("dialog")
  expect(within(dlg).getByRole("button", { name: "Delete dev" })).toBeInTheDocument()
  expect(within(dlg).queryByRole("button", { name: "Delete main" })).not.toBeInTheDocument()
})

it("mock: delete branch: confirm deletes it and refreshes the list", async () => {
  const mock = await hello("alice")
  at("/alice/hello", mock)
  await userEvent.click(await screen.findByRole("button", { name: "Delete branch" }))
  await userEvent.click(screen.getByRole("button", { name: "Delete dev" }))
  const dlg = screen.getByRole("alertdialog")
  expect(dlg).toHaveTextContent("dev")
  await userEvent.click(within(dlg).getByRole("button", { name: "Delete branch" }))
  expect(await screen.findByText("Branch dev deleted.")).toBeInTheDocument()
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument()
  await vi.waitFor(() => expect(screen.queryByRole("button", { name: "Delete branch" })).not.toBeInTheDocument())
  expect(await branchNames(mock)).toEqual(["main"])
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello")
})

it("mock: delete branch: deleting the current ref moves to the default branch", async () => {
  at("/alice/hello/tree/dev", await hello("alice"))
  await userEvent.click(await screen.findByRole("button", { name: "Delete branch" }))
  await userEvent.click(screen.getByRole("button", { name: "Delete dev" }))
  await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete branch" }))
  await vi.waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/tree/main"))
})

// The session ends behind the page's back (mock.logout() on the same instance), then a write.
const expireThen = async (mock: MockApiService) => {
  await mock.logout()
  await tick()
}

it("mock: delete branch: 401 expires the session with a toast and hides owner actions", async () => {
  const mock = await hello("alice")
  at("/alice/hello", mock)
  await userEvent.click(await screen.findByRole("button", { name: "Delete branch" }))
  await userEvent.click(screen.getByRole("button", { name: "Delete dev" }))
  await expireThen(mock)
  await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete branch" }))
  await expectLoggedOut()
  await vi.waitFor(() => {
    expect(screen.queryByRole("button", { name: "Delete branch" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Create branch" })).not.toBeInTheDocument()
  })
  expect(await branchNames(mock)).toEqual(["dev", "main"])
})

it("mock: create branch: 401 expires the session with a toast and hides owner actions", async () => {
  const mock = await hello("alice")
  at("/alice/hello", mock)
  await userEvent.type(await openCreate(), "feature")
  await expireThen(mock)
  await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Create branch" }))
  await expectLoggedOut()
  await vi.waitFor(() => {
    expect(screen.queryByRole("button", { name: "Delete branch" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Create branch" })).not.toBeInTheDocument()
  })
})

it("mock: delete repo: 401 expires the session with a toast and stays on the page", async () => {
  const mock = await hello("alice")
  at("/alice/hello/settings", mock)
  await userEvent.click(await screen.findByRole("button", { name: "Delete repository" }))
  await expireThen(mock)
  await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete repository" }))
  await expectLoggedOut()
  await vi.waitFor(() => expect(screen.queryByRole("button", { name: "Delete repository" })).not.toBeInTheDocument())
  expect(screen.queryByRole("button", { name: "Create branch" })).not.toBeInTheDocument()
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello")
})

it("mock: file viewer shows name, size, ref, breadcrumbs and exact content", async () => {
  at("/alice/hello/blob/main/src/main.go", await hello(null))
  expect(await screen.findByRole("heading", { name: "main.go" })).toBeInTheDocument()
  expect(screen.getByTestId("file-content").textContent).toBe("package main\n")
  expect(screen.getByText(/13 bytes/)).toBeInTheDocument()
  expect(screen.getByText("main", { selector: "span.font-mono" })).toHaveClass("max-sm:break-all")
  const crumbs = screen.getByRole("navigation", { name: "Breadcrumbs" })
  expect(within(crumbs).getAllByRole("link").map((l) => l.getAttribute("href"))).toEqual([
    "/alice/hello/tree/main",
    "/alice/hello/tree/main/src",
  ])
  expect(screen.queryByRole("link", { name: "Edit" })).not.toBeInTheDocument()
})

it("mock: file viewer shows markup as text, whitespace intact", async () => {
  at("/alice/hello/blob/main/a.txt", await hello(null))
  await screen.findByRole("heading", { name: "a.txt" })
  expect(screen.getByTestId("file-content").textContent).toBe("  <b>hi</b>\n\tindent  \n")
})

it("mock: file viewer: Edit only for the owner on a branch", async () => {
  const mock = await hello("alice")
  const view = at("/alice/hello/blob/main/a.txt", mock)
  expect(await screen.findByRole("link", { name: "Edit" })).toHaveAttribute("href", "/alice/hello/edit/main/a.txt")
  view.unmount()
  at(`/alice/hello/blob/${await mainTip(mock)}/a.txt`, mock)
  await screen.findByRole("heading", { name: "a.txt" })
  expect(screen.queryByRole("link", { name: "Edit" })).not.toBeInTheDocument()
})

it("mock: file viewer: empty file note, no content block", async () => {
  at("/alice/hello/blob/main/e", await hello(null))
  expect(await screen.findByText(/empty file/i)).toBeInTheDocument()
  expect(screen.queryByTestId("file-content")).not.toBeInTheDocument()
})

it.each([
  ["a directory", "src", /not a file/i],
  ["a missing path", "nope", /path not found/i],
])("mock: file viewer on %s shows a not-found state", async (_w, path, msg) => {
  at(`/alice/hello/blob/main/${path}`, await hello(null))
  expect(await screen.findByTestId("not-found")).toHaveTextContent(msg)
})

it("mock: file viewer on an unknown ref shows a not-found state", async () => {
  at("/alice/hello/blob/nope/a.txt", await hello(null))
  expect(await screen.findByTestId("not-found")).toHaveTextContent("ref not found")
})

it("mock: delete repo: cancel keeps the repository and sends no delete", async () => {
  const mock = await hello("alice")
  const del = vi.spyOn(mock, "deleteRepo")
  at("/alice/hello/settings", mock)
  await userEvent.click(await screen.findByRole("button", { name: "Delete repository" }))
  await userEvent.click(screen.getByRole("button", { name: "Cancel" }))
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument()
  expect(del).not.toHaveBeenCalled()
})

it("mock: delete repo: a 403 is shown in the dialog and the page stays (session switched to another user)", async () => {
  const mock = await hello("alice")
  at("/alice/hello/settings", mock)
  await userEvent.click(await screen.findByRole("button", { name: "Delete repository" }))
  await mock.logout()
  await mock.login("bob", "password1")
  await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete repository" }))
  expect(await within(screen.getByRole("alertdialog")).findByRole("alert")).toHaveTextContent("forbidden")
  expect(screen.getByTestId("loc")).toHaveTextContent("/alice/hello/settings")
  expect((await mock.getRepo("alice", "hello")).name).toBe("hello")
})

it("mock: delete branch: cancel keeps the branch", async () => {
  const mock = await hello("alice")
  at("/alice/hello", mock)
  await userEvent.click(await screen.findByRole("button", { name: "Delete branch" }))
  await userEvent.click(screen.getByRole("button", { name: "Delete dev" }))
  await userEvent.click(screen.getByRole("button", { name: "Cancel" }))
  expect(await branchNames(mock)).toEqual(["dev", "main"])
})

it("mock: delete branch: a 404 (already deleted elsewhere) is shown inline", async () => {
  const mock = await hello("alice")
  at("/alice/hello", mock)
  await userEvent.click(await screen.findByRole("button", { name: "Delete branch" }))
  await userEvent.click(screen.getByRole("button", { name: "Delete dev" }))
  await mock.deleteBranch("alice", "hello", "dev")
  await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete branch" }))
  expect(await within(screen.getByRole("alertdialog")).findByRole("alert")).toHaveTextContent("branch not found")
})

it("500 shows an error with retry that reloads", async () => {
  const mock = await hello(null)
  failGet(mock, /\/repos\/alice\/hello$/)
  at("/alice/hello", mock)
  expect(await screen.findByRole("alert")).toHaveTextContent("internal error")
  await userEvent.click(screen.getByRole("button", { name: "Retry" }))
  expect(await screen.findByRole("table", { name: "Files" })).toBeInTheDocument()
})

// Git cannot store an empty directory, so the answer is shaped on the instance.
it("an empty directory shows a not-found state", async () => {
  const mock = await hello(null)
  alterGet<{ entries: unknown[] }>(mock, "/tree/main/src", (t) => ({ ...t, entries: [] }))
  at("/alice/hello/tree/main/src", mock)
  expect(await screen.findByTestId("not-found")).toBeInTheDocument()
})

// The UI never offers the default branch; the 400 the service answers for it is injected for the dev branch.
it("delete branch: a 400 from the server is shown inline", async () => {
  const mock = await hello("alice")
  failNext(mock, "deleteBranch", 400, "cannot delete the default branch")
  at("/alice/hello", mock)
  await userEvent.click(await screen.findByRole("button", { name: "Delete branch" }))
  await userEvent.click(screen.getByRole("button", { name: "Delete dev" }))
  await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete branch" }))
  expect(await within(screen.getByRole("alertdialog")).findByRole("alert")).toHaveTextContent("cannot delete the default branch")
  expect(await branchNames(mock)).toEqual(["dev", "main"])
})

it("file viewer: loading and error with retry", async () => {
  const mock = await hello(null)
  const release = failGetAfterHold(mock, "/blob/main/a.txt")
  at("/alice/hello/blob/main/a.txt", mock)
  expect(await screen.findByRole("status", { name: "Loading file" })).toBeInTheDocument()
  release()
  expect(await screen.findByRole("alert")).toHaveTextContent("internal error")
  await userEvent.click(screen.getByRole("button", { name: "Retry" }))
  expect(await screen.findByRole("heading", { name: "a.txt" })).toBeInTheDocument()
})

it("starts branches, tree and commits without waiting for the repo; README waits for the tree", async () => {
  const mock = await hello(null)
  const release = holdGet(mock, /\/repos\/alice\/hello$/)
  at("/alice/hello/tree/main", mock)
  await vi.waitFor(() => expect(readPaths(mock).length).toBeGreaterThanOrEqual(4))
  for (const k of ["/branches", "/tree/main", "/commits?ref=main&page=1"]) {
    expect(readPaths(mock).some((c) => c.endsWith(k))).toBe(true)
  }
  release()
  expect(await screen.findByRole("table", { name: "Files" })).toBeInTheDocument()
  await vi.waitFor(() => expect(readPaths(mock).some((c) => c.endsWith("/blob/main/README.md"))).toBe(true))
})

it("without a ref in the URL, commits wait for the repo (empty repos have none); branches do not", async () => {
  const mock = await hello(null)
  const release = holdGet(mock, /\/repos\/alice\/hello$/)
  at("/alice/hello", mock)
  await vi.waitFor(() => expect(readPaths(mock).some((c) => c.endsWith("/branches"))).toBe(true))
  expect(readPaths(mock).some((c) => c.includes("/commits"))).toBe(false) // needs repo.empty
  expect(readPaths(mock).some((c) => c.includes("/tree/"))).toBe(false) // needs the default branch
  release()
  expect(await screen.findByRole("table", { name: "Files" })).toBeInTheDocument()
})
