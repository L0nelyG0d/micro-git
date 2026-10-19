import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, it, vi } from "vitest"
import { failNext, holdNext, renderWithMock } from "@/test-mock"
import { MockApiService } from "@/services/MockApiService"
import type { Change } from "@/lib/git"
import { formatDate } from "@/lib/format"

/** Logged-out visitor on a mock where alice/hello got these commits in order; returns the oids. */
async function withCommits(...commits: { message: string; changes: Change[] }[]) {
  const mock = new MockApiService()
  await mock.register("alice", "password1")
  await mock.login("alice", "password1")
  await mock.createRepo({ name: "hello" })
  const oids: string[] = []
  let tip = ""
  for (const c of commits) {
    tip = (await mock.createCommit("alice", "hello", { branch: "main", expected_tip: tip, ...c })).oid
    oids.push(tip)
  }
  await mock.logout()
  return { mock, oids }
}
const put = (path: string, content: string): Change => ({ path, content })
const main = () => within(screen.getByRole("main"))

it("a page past the end shows an empty state", async () => {
  const { mock } = await withCommits({ message: "first", changes: [put("a.txt", "a")] })
  renderWithMock("/alice/hello/commits/main?page=9", mock)
  expect(await screen.findByText(/no more commits/i)).toBeInTheDocument()
  expect(screen.getByRole("link", { name: "Go to the first page" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Next" })).toBeDisabled()
})

it("unknown ref is not found", async () => {
  const { mock } = await withCommits({ message: "first", changes: [put("a.txt", "a")] })
  renderWithMock("/alice/hello/commits/nope", mock)
  expect(await screen.findByText("History not found")).toBeInTheDocument()
})

it("empty repo shows the message", async () => {
  const { mock } = await withCommits()
  renderWithMock("/alice/hello/commits/main", mock)
  expect(await screen.findByText(/repository is empty/i)).toBeInTheDocument()
})

it("a commit oid works as ref", async () => {
  const { mock, oids } = await withCommits({ message: "first", changes: [put("a.txt", "a")] })
  renderWithMock(`/alice/hello/commits/${oids[0]}`, mock)
  expect(await screen.findByRole("link", { name: oids[0].slice(0, 7) })).toBeInTheDocument()
  expect(main().getByText(/read-only/)).toBeInTheDocument()
})


it("root commit shows as such, and a later commit links to its parent with added, modified and deleted files", async () => {
  const { mock, oids } = await withCommits(
    { message: "first", changes: [put("src/a.txt", "keep\nold <script>x</script>\n"), put("gone.txt", "bye\n")] },
    {
      message: "second\n\nbody text",
      changes: [put("src/a.txt", "keep\nnew\n"), put("z.txt", "hi\n"), { path: "gone.txt", delete: true }],
    },
  )
  const view = renderWithMock(`/alice/hello/commit/${oids[0]}`, mock)
  expect(await screen.findByText("root commit")).toBeInTheDocument()
  view.unmount()

  renderWithMock(`/alice/hello/commit/${oids[1]}`, mock)
  expect(await screen.findByRole("heading", { level: 1 })).toHaveTextContent("second")
  expect(main().getByText(/body text/)).toBeInTheDocument()
  expect(screen.getByRole("link", { name: oids[0].slice(0, 7) })).toHaveAttribute("href", `/alice/hello/commit/${oids[0]}`)
  expect(main().getByTestId("summary")).toHaveTextContent("3 files changed")
  const status = (path: string) => within(screen.getByRole("region", { name: path })).getByTestId("file-status")
  expect(status("src/a.txt")).toHaveTextContent("modified")
  expect(status("z.txt")).toHaveTextContent("added")
  expect(status("gone.txt")).toHaveTextContent("deleted")
  const mod = within(screen.getByRole("region", { name: "src/a.txt" }))
  expect(mod.getByTestId("line-add")).toHaveTextContent("+new")
  expect(mod.getByTestId("line-del")).toHaveTextContent("-old <script>x</script>")
  expect(document.querySelector("script")).toBeNull()
})

it.each([
  ["an invalid commit id", "nope"],
  ["an unknown commit", "c".repeat(40)],
])("%s shows not found", async (_what, oid) => {
  const { mock } = await withCommits({ message: "first", changes: [put("a.txt", "a")] })
  renderWithMock(`/alice/hello/commit/${oid}`, mock)
  expect(await screen.findByText("Commit not found")).toBeInTheDocument()
})

it("lists commits with first line, author, date and full-oid link", async () => {
  const { mock, oids } = await withCommits({ message: "Add readme\n\nlong body", changes: [put("README.md", "# hi\n")] })
  const when = (await mock.listCommits("alice", "hello", { ref: "main" })).commits[0].time
  renderWithMock("/alice/hello/commits/main", mock)
  const link = await screen.findByRole("link", { name: oids[0].slice(0, 7) })
  expect(link).toHaveAttribute("href", `/alice/hello/commit/${oids[0]}`)
  expect(main().getByText("main", { selector: "span.font-mono" })).toHaveClass("max-sm:break-all")
  expect(main().getByText("Add readme")).toBeInTheDocument()
  expect(main().queryByText(/long body/)).not.toBeInTheDocument()
  expect(main().getByText("alice")).toBeInTheDocument()
  expect(main().getByText(formatDate(when))).toBeInTheDocument()
})

it("paginates with page in the URL: Previous disabled on page 1, Next follows has_more", async () => {
  const { mock } = await withCommits(
    ...Array.from({ length: 51 }, (_, i) => ({ message: `c${i}`, changes: [put("a.txt", String(i))] })),
  )
  renderWithMock("/alice/hello/commits/main", mock)
  await screen.findByText("c50")
  expect(screen.getByRole("button", { name: "Previous" })).toBeDisabled()
  await userEvent.click(screen.getByRole("link", { name: "Next" }))
  expect(await screen.findByText("Page 2")).toBeInTheDocument()
  expect(await screen.findByText("c0")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Next" })).toBeDisabled()
  expect(screen.getByRole("link", { name: "Previous" })).toHaveAttribute("href", "/alice/hello/commits/main")
})

// A commit without files cannot be created (the mock and the backend reject an empty commit), so the answer is shaped on the instance.
it("commit with no files", async () => {
  const { mock, oids } = await withCommits({ message: "first", changes: [put("a.txt", "a")] })
  const real = await mock.getCommit("alice", "hello", oids[0])
  vi.spyOn(mock, "getCommit").mockResolvedValueOnce({ ...real, files: [] })
  renderWithMock(`/alice/hello/commit/${oids[0]}`, mock)
  expect(await screen.findByText("no files changed")).toBeInTheDocument()
  expect(main().getByTestId("summary")).toHaveTextContent("0 files changed, 0 additions, 0 deletions")
})
it("shows a loading state while the history request is pending", async () => {
  const { mock } = await withCommits({ message: "first", changes: [put("a.txt", "a")] })
  const release = holdNext(mock, "getRepo")
  renderWithMock("/alice/hello/commits/main", mock)
  expect(await main().findByRole("status", { name: "Loading history" })).toBeInTheDocument()
  release()
  expect(await screen.findByText("first")).toBeInTheDocument()
})

it("500 shows an error that retries", async () => {
  const { mock, oids } = await withCommits({ message: "first", changes: [put("a.txt", "a")] })
  failNext(mock, "listCommits")
  renderWithMock("/alice/hello/commits/main", mock)
  expect(await screen.findByRole("alert")).toHaveTextContent("internal error")
  await userEvent.click(screen.getByRole("button", { name: "Retry" }))
  expect(await screen.findByRole("link", { name: oids[0].slice(0, 7) })).toBeInTheDocument()
})

it("500 on commit detail shows retry", async () => {
  const { mock, oids } = await withCommits({ message: "first", changes: [put("a.txt", "a")] })
  failNext(mock, "getCommit")
  renderWithMock(`/alice/hello/commit/${oids[0]}`, mock)
  expect(await screen.findByRole("alert")).toHaveTextContent("internal error")
  await userEvent.click(screen.getByRole("button", { name: "Retry" }))
  expect(await screen.findByText("root commit")).toBeInTheDocument()
})

it("commit detail shows hunk headers for modified, added and deleted files", async () => {
  const { mock, oids } = await withCommits(
    { message: "first", changes: [put("m.txt", "a\nb\nc\n"), put("gone.txt", "bye\n")] },
    { message: "second", changes: [put("m.txt", "a\nB\nc\n"), put("z.txt", "hi\n"), { path: "gone.txt", delete: true }] },
  )
  renderWithMock(`/alice/hello/commit/${oids[1]}`, mock)
  const header = async (path: string) => within(await screen.findByRole("region", { name: path })).getByTestId("hunk-header")
  expect(await header("m.txt")).toHaveTextContent("@@ -1,3 +1,3 @@")
  expect(await header("z.txt")).toHaveTextContent("@@ +1,1 @@")
  expect(await header("gone.txt")).toHaveTextContent("@@ -1,1 @@")
})

it("a root commit with a file over the diff limit shows it as too large, and a final-newline-only change shows no visible changes", async () => {
  const big = "x\n".repeat(5001)
  const { mock, oids } = await withCommits(
    { message: "first", changes: [put("big.txt", big), put("nl.txt", "same\n")] },
    { message: "second", changes: [put("nl.txt", "same")] },
  )
  const first = renderWithMock(`/alice/hello/commit/${oids[0]}`, mock)
  expect(await screen.findByText("root commit")).toBeInTheDocument()
  expect(main().getByText("diff too large to display")).toBeInTheDocument()
  first.unmount()
  renderWithMock(`/alice/hello/commit/${oids[1]}`, mock)
  expect(await screen.findByText("no visible changes (only the final newline differs)")).toBeInTheDocument()
})
