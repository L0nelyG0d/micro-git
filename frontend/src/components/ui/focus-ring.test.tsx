import { render, screen } from "@testing-library/react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Select, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog"

// Issue #120: the keyboard focus ring must be the full-opacity --ring token
// (about 4.9:1 on the header), not a 50% tint (about 2:1). jsdom has no layout
// or Tailwind, so these check the classes the shared primitives emit.
function classes(el: HTMLElement): string[] {
  return el.className.split(/\s+/)
}

function expectSolidFocusRing(el: HTMLElement, color: string) {
  const c = classes(el)
  expect(c).toContain("focus-visible:ring-[3px]")
  expect(c).toContain(`focus-visible:ring-${color}`)
  expect(c.filter((x) => x.startsWith("focus-visible:ring-") && x.includes("/"))).toEqual([])
  expect(c.filter((x) => x.startsWith("focus:ring"))).toEqual([])
}

describe("focus rings on shared primitives (#120)", () => {
  it("default Button uses the solid ring token", () => {
    render(<Button>Save</Button>)
    expectSolidFocusRing(screen.getByRole("button", { name: "Save" }), "ring")
  })

  it("destructive Button uses the solid destructive colour", () => {
    render(<Button variant="destructive">Delete</Button>)
    const el = screen.getByRole("button", { name: "Delete" })
    const c = classes(el)
    expect(c).toContain("focus-visible:ring-destructive")
    expect(c.filter((x) => /focus-visible:ring-destructive\//.test(x) || /dark:focus-visible:ring-destructive\//.test(x))).toEqual([])
  })

  it("Input and Textarea use the solid ring token", () => {
    render(
      <>
        <Input aria-label="name" />
        <Textarea aria-label="body" />
      </>
    )
    expectSolidFocusRing(screen.getByLabelText("name"), "ring")
    expectSolidFocusRing(screen.getByLabelText("body"), "ring")
  })

  it("Select trigger uses the solid ring token", () => {
    render(
      <Select>
        <SelectTrigger aria-label="branch">
          <SelectValue />
        </SelectTrigger>
      </Select>
    )
    expectSolidFocusRing(screen.getByRole("combobox", { name: "branch" }), "ring")
  })

  it("dialog close X shows its ring on keyboard focus only", () => {
    render(
      <Dialog open>
        <DialogContent>
          <DialogTitle>t</DialogTitle>
          <DialogDescription>d</DialogDescription>
        </DialogContent>
      </Dialog>
    )
    const x = screen.getByRole("button", { name: "Close" })
    const c = classes(x)
    expect(c).toContain("focus-visible:ring-2")
    expect(c).toContain("focus-visible:ring-ring")
    expect(c.filter((n) => n.startsWith("focus:ring"))).toEqual([])
  })
})
