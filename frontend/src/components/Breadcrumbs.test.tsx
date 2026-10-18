import { MemoryRouter } from "@/test-utils"
import { render, screen } from "@testing-library/react"
import { expect, it } from "vitest"
import { Breadcrumbs } from "./Breadcrumbs"

const LONG = "r".repeat(40)

function renderCrumbs(path: string, lastIsFile?: boolean) {
  render(
    <MemoryRouter>
      <Breadcrumbs owner="alice" repo={LONG} ref_="main" path={path} lastIsFile={lastIsFile} />
    </MemoryRouter>,
  )
  return screen.getByRole("navigation", { name: "Breadcrumbs" })
}

it("below 640px every segment may shrink and break a long unbroken name", () => {
  const nav = renderCrumbs("d".repeat(30) + "/" + "f".repeat(50) + ".txt", true)
  expect(nav).toHaveClass("max-sm:min-w-0")
  const repo = screen.getByRole("link", { name: LONG })
  expect(repo).toHaveClass("max-sm:break-all")
  expect(repo).toHaveClass("max-sm:min-w-0")
  expect(repo).toHaveAttribute("href", "/alice/" + LONG + "/tree/main")
  const dir = screen.getByRole("link", { name: "d".repeat(30) })
  expect(dir).toHaveClass("max-sm:break-all")
  expect(dir.parentElement).toHaveClass("max-sm:min-w-0")
  const file = screen.getByText("f".repeat(50) + ".txt")
  expect(file).toHaveAttribute("aria-current", "page")
  expect(file).toHaveClass("max-sm:break-all")
})

it("keeps the landmark and exposes no sm: overrides at >=640px", () => {
  const nav = renderCrumbs("src")
  expect(nav.className).not.toMatch(/(^|\s)(break-all|min-w-0|truncate)(\s|$)/)
})
