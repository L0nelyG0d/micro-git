import { MemoryRouter } from "@/test-utils"
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { RepoRows } from "@/components/RepoRows"
import type { Repo } from "@/lib/repos"

const repo = (o: Partial<Repo>): Repo => ({
  id: 1,
  owner: "alice",
  name: "hello",
  description: "A greeting",
  default_branch: "main",
  empty: true,
  private: false,
  created_at: "2026-01-02T15:04:05Z",
  ...o,
})
const repos = [
  repo({ id: 1, name: "hello", description: "A greeting" }),
  repo({ id: 2, name: "Secret-Plans", description: "World domination", private: true }),
  repo({ id: 3, owner: "bob", name: "tools", description: "" }),
]

function renderRows(showOwner?: boolean) {
  render(
    <MemoryRouter>
      <RepoRows repos={repos} showOwner={showOwner} />
    </MemoryRouter>,
  )
  return screen.getByRole("searchbox", { name: "Find a repository" })
}
const listed = () =>
  within(screen.getByRole("list", { name: "Repository list" }))
    .getAllByRole("link")
    .map((a) => a.textContent)

describe("RepoRows filter", () => {
  it("shows every row, with owner/name by default", () => {
    renderRows()
    expect(listed()).toEqual(["alice/hello", "alice/Secret-Plans", "bob/tools"])
  })

  it("prints only the name when showOwner is false", () => {
    renderRows(false)
    expect(listed()).toEqual(["hello", "Secret-Plans", "tools"])
  })

  it.each([
    { query: "hello", want: ["alice/hello"], why: "name" },
    { query: "WORLD", want: ["alice/Secret-Plans"], why: "description, case-insensitive" },
    { query: "secret-plans", want: ["alice/Secret-Plans"], why: "name, case-insensitive" },
    { query: "bob/", want: ["bob/tools"], why: "owner" },
    { query: "  tools  ", want: ["bob/tools"], why: "surrounding whitespace is trimmed" },
    { query: "alice", want: ["alice/hello", "alice/Secret-Plans"], why: "several matches keep order" },
  ])("typing '$query' matches on $why", async ({ query, want }) => {
    const box = renderRows()
    await userEvent.type(box, query)
    expect(listed()).toEqual(want)
  })

  it("shows a message with the query when nothing matches", async () => {
    const box = renderRows()
    await userEvent.type(box, "zzz")
    expect(screen.getByText("No repositories match “zzz”.")).toBeInTheDocument()
    expect(screen.queryByRole("list", { name: "Repository list" })).not.toBeInTheDocument()
  })

  it.each(["clearing the query", "a whitespace-only query"])("%s shows all rows", async (how) => {
    const box = renderRows()
    await userEvent.type(box, "hello")
    expect(listed()).toHaveLength(1)
    await userEvent.clear(box)
    if (how.startsWith("a whitespace")) await userEvent.type(box, "   ")
    expect(listed()).toHaveLength(3)
  })

  it("shows a Public or Private badge per row", () => {
    renderRows()
    const rows = within(screen.getByRole("list", { name: "Repository list" })).getAllByRole("listitem")
    expect(within(rows[0]).getByText("Public")).toBeInTheDocument()
    expect(within(rows[1]).getByText("Private")).toBeInTheDocument()
  })
})
