import { render } from "@testing-library/react"
import { expect, it } from "vitest"
import { renderMarkdown } from "@/lib/markdown"

function html(src: string) {
  const { container } = render(<div>{renderMarkdown(src)}</div>)
  return container
}

it("renders headings 1 to 6", () => {
  const c = html("# a\n## b\n### c\n#### d\n##### e\n###### f")
  for (let n = 1; n <= 6; n++) expect(c.querySelector(`h${n}`)).not.toBeNull()
  expect(c.querySelector("h1")?.textContent).toBe("a")
})

it("separates paragraphs on blank lines", () => {
  const c = html("one\ntwo\n\nthree")
  const ps = c.querySelectorAll("p")
  expect(ps).toHaveLength(2)
  expect(ps[0].textContent).toBe("one\ntwo")
})

it("renders bullet and numbered lists", () => {
  const c = html("- a\n* b\n\n1. x\n2. y")
  expect([...c.querySelectorAll("ul li")].map((l) => l.textContent)).toEqual(["a", "b"])
  expect([...c.querySelectorAll("ol li")].map((l) => l.textContent)).toEqual(["x", "y"])
})

it("renders fenced code verbatim in a scrollable mono block", () => {
  const c = html("```js\nconst a = **1**\n# not heading\n```\nafter")
  const pre = c.querySelector("pre")!
  expect(pre.textContent).toBe("const a = **1**\n# not heading")
  expect(pre.className).toContain("font-mono")
  expect(pre.className).toContain("overflow-x-auto")
  expect(c.querySelector("p")?.textContent).toBe("after")
})

it("renders inline code, bold and italic", () => {
  const c = html("a `x*y*` **b** *i* z")
  expect(c.querySelector("code")?.textContent).toBe("x*y*")
  expect(c.querySelector("strong")?.textContent).toBe("b")
  expect(c.querySelector("em")?.textContent).toBe("i")
})

it("keeps unsupported syntax as plain text", () => {
  const src = "[link](http://x) ![img](y.png)\n> quote\n| a | b |"
  const c = html(src)
  expect(c.querySelector("a, img, blockquote, table")).toBeNull()
  expect(c.textContent).toBe(src)
})

it("shows HTML and script payloads as text and creates no such elements", () => {
  const src = "<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n- <b>x</b>"
  const c = html(src)
  expect(c.querySelector("script, img, b")).toBeNull()
  expect(c.textContent).toContain("<script>alert(1)</script>")
  expect(c.textContent).toContain("<img src=x onerror=alert(1)>")
  expect(c.textContent).toContain("<b>x</b>")
})

it("renders nothing for an empty file", () => {
  expect(renderMarkdown("")).toEqual([])
  expect(renderMarkdown("\n\n")).toEqual([])
})
