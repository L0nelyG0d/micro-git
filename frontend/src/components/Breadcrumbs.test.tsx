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

it("every segment may shrink and break a long unbroken name", () => {
  const nav = renderCrumbs("d".repeat(30) + "/" + "f".repeat(50) + ".txt", true)
  expect(nav).toHaveClass("min-w-0")
  const repo = screen.getByRole("link", { name: LONG })
  expect(repo).toHaveClass("break-all")
  expect(repo).toHaveClass("min-w-0")
  expect(repo).toHaveAttribute("href", "/alice/" + LONG + "/tree/main")
  const dir = screen.getByRole("link", { name: "d".repeat(30) })
  expect(dir).toHaveClass("break-all")
  expect(dir.parentElement).toHaveClass("min-w-0")
  const file = screen.getByText("f".repeat(50) + ".txt")
  expect(file).toHaveAttribute("aria-current", "page")
  expect(file).toHaveClass("break-all")
})

it("keeps the landmark and does not truncate", () => {
  const nav = renderCrumbs("src")
  expect(nav.className).not.toMatch(/(^|\s)(truncate)(\s|$)/)
})
