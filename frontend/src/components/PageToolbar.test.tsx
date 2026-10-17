import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom"
import { PageToolbar } from "@/components/PageToolbar"

function Where() {
  return <p data-testid="where">{useLocation().pathname}</p>
}

function setup() {
  render(
    <MemoryRouter initialEntries={["/a/b/blob/main/x.txt"]}>
      <PageToolbar up={{ to: "/a/b", label: "files" }}>
        <span>crumbs</span>
      </PageToolbar>
      <Routes>
        <Route path="*" element={<Where />} />
      </Routes>
    </MemoryRouter>,
  )
}

it("shows Back, Up and the breadcrumbs slot in a labelled toolbar", () => {
  setup()
  const bar = screen.getByRole("toolbar", { name: "Page navigation" })
  expect(bar).toHaveTextContent("crumbs")
  expect(screen.getByRole("link", { name: "Up" })).toHaveAttribute("href", "/a/b")
  expect(screen.getByRole("button", { name: "Back" })).toBeEnabled()
})

it("Back falls back to the parent when the page was opened directly", async () => {
  setup()
  await userEvent.click(screen.getByRole("button", { name: "Back" }))
  expect(screen.getByTestId("where")).toHaveTextContent("/a/b")
})
