import { render, screen } from "@testing-library/react"
import { MemoryRouter } from "react-router-dom"
import { expect, it, vi } from "vitest"
import { Nav } from "@/components/Nav"

vi.mock("@/auth/AuthContext", () => ({
  useAuth: () => ({ user: null, loading: false, sessionError: true, logout: vi.fn() }),
}))

// jsdom has no layout, so the 320px overflow itself is measured in a real browser (#119);
// here we pin the classes that prevent it.
it("swaps the session label for a short one below 360px and keeps the full phrase in the title", () => {
  render(
    <MemoryRouter>
      <Nav />
    </MemoryRouter>,
  )
  const status = screen.getByRole("status")
  expect(status).toHaveAttribute("title", expect.stringContaining("Session unavailable"))
  expect(screen.getByText("Session unavailable")).toHaveClass("max-[359px]:hidden")
  expect(screen.getByText("Offline")).toHaveClass("min-[360px]:hidden")
})
