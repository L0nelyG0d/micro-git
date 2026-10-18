import { render, screen } from "@testing-library/react"
import { MemoryRouter } from "react-router-dom"
import { expect, it } from "vitest"
import { RepoHeader } from "@/components/RepoHeader"
import type { Repo } from "@/lib/repos"

const repo: Repo = {
  id: 1,
  owner: "alice",
  name: "hello",
  description: "",
  default_branch: "main",
  private: false,
  empty: false,
  created_at: "2026-01-02T15:04:05Z",
}

function renderHeader() {
  render(
    <MemoryRouter>
      <RepoHeader repo={repo} active="commits" owns />
    </MemoryRouter>,
  )
  return screen.getByRole("navigation", { name: "Repository" })
}

// jsdom cannot measure layout, so pin the markup that keeps every tab on screen at 320 px (#71).
it("tab strip wraps instead of clipping and keeps order and aria-current", () => {
  const nav = renderHeader()
  expect(nav).toHaveClass("flex-wrap")
  expect(nav).not.toHaveClass("overflow-x-auto")
  const links = Array.from(nav.querySelectorAll("a"))
  expect(links.map((a) => a.textContent)).toEqual(["Code", "Commits", "Compare", "Settings"])
  expect(links.map((a) => a.getAttribute("aria-current"))).toEqual([null, "page", null, null])
})

it("tabs are tighter below sm and icons are decorative and hidden on narrow screens", () => {
  const nav = renderHeader()
  for (const a of Array.from(nav.querySelectorAll("a"))) {
    expect(a).toHaveClass("px-2", "sm:px-3")
    const icon = a.querySelector("svg")
    expect(icon).toHaveAttribute("aria-hidden", "true")
    expect(icon).toHaveClass("hidden", "sm:block")
  }
})
