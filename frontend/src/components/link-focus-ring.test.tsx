import { readFileSync } from "node:fs"
import { resolve } from "node:path"

// Issue #120: links (logo, avatar, plain text links) get the shared ring from
// one base rule in index.css. jsdom does not run Tailwind, so check the rule.
describe("link focus ring (#120)", () => {
  const css = readFileSync(resolve(__dirname, "../index.css"), "utf8")
  const rule = /a:focus-visible\s*\{([^}]*)\}/.exec(css)?.[1] ?? ""

  it("has one a:focus-visible base rule with the solid 3px ring token", () => {
    expect(rule).toContain("ring-[3px]")
    expect(rule).toContain("ring-ring")
    expect(rule).toContain("outline-none")
    expect(rule).not.toMatch(/ring-ring\//)
  })

  it("does not ring on plain :focus (mouse click)", () => {
    expect(css).not.toMatch(/a:focus\s*[{,]/)
  })
})
