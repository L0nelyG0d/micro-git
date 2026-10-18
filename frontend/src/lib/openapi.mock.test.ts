/// <reference types="node" />
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { beforeEach, describe, expect, it } from "vitest"
import { MockApiService } from "@/services/MockApiService"

// The mock service must answer like openapi.yaml says. For each of the
// operations, the schema behind its 2xx application/json response is found
// with the same kind of line scanner as openapi.test.ts (no YAML library):
// the mock result must be of the schema's top-level type (object or array)
// and have every key under `required:`. Only the top level is checked.
//
// Supported YAML subset (anything else throws, so the test never passes on a
// misread spec): block mappings, `$ref: '#/components/schemas/X'`, flow lists
// `required: [a, b]`, and `allOf:` lists of `- $ref` / inline `- type:` items.

const specFile = resolve(process.cwd(), "../openapi.yaml")

const indentOf = (line: string) => line.length - line.trimStart().length
const blank = (line: string) => line.trim() === "" || line.trimStart().startsWith("#")

/** Lines nested deeper than the line at `start` (its block, without itself). */
function blockAfter(lines: string[], start: number): string[] {
  const base = indentOf(lines[start])
  const out: string[] = []
  for (let i = start + 1; i < lines.length; i++) {
    if (!blank(lines[i]) && indentOf(lines[i]) <= base) break
    out.push(lines[i])
  }
  return out
}

/** Index of the first line of `lines` that is exactly `key:` / `key: ...` at `indent`. */
function findKey(lines: string[], indent: number, key: string): number {
  return lines.findIndex((l) => indentOf(l) === indent && !blank(l) && (l.trim() === `${key}:` || l.trim().startsWith(`${key}: `)))
}

const unquote = (s: string) => s.trim().replace(/^'(.*)'$/, "$1").replace(/^"(.*)"$/, "$1")

/** Names of the schemas in `components.schemas` mapped to their block. */
function componentSchema(yaml: string[], name: string): string[] {
  const comp = findKey(yaml, 0, "components")
  if (comp < 0) throw new Error("no components section")
  const compBlock = blockAfter(yaml, comp)
  const schemas = findKey(compBlock, 2, "schemas")
  if (schemas < 0) throw new Error("no components.schemas section")
  const schemaBlock = blockAfter(compBlock, schemas)
  const at = findKey(schemaBlock, 4, name)
  if (at < 0) throw new Error(`schema ${name} not found`)
  return blockAfter(schemaBlock, at)
}

type Shape = { type?: string; required: string[] }

/** Type and required keys of one schema block (the lines inside it). */
function summarize(yaml: string[], block: string[]): Shape {
  const content = block.filter((l) => !blank(l))
  if (content.length === 0) throw new Error("empty schema")
  const base = indentOf(content[0])
  const direct = content.filter((l) => indentOf(l) === base)
  const shape: Shape = { required: [] }

  const ref = direct.find((l) => l.trim().startsWith("$ref:"))
  if (ref) {
    const m = /^\$ref:\s*(.+)$/.exec(ref.trim())
    const name = unquote(m?.[1] ?? "").replace("#/components/schemas/", "")
    if (!name || name.includes("/")) throw new Error(`unsupported $ref: ${ref.trim()}`)
    return summarize(yaml, componentSchema(yaml, name))
  }

  for (const l of direct) {
    const t = l.trim()
    if (t.startsWith("type:")) shape.type = unquote(t.slice(5))
    if (t.startsWith("required:")) shape.required.push(...flowList(t))
  }

  const allOf = content.findIndex((l) => indentOf(l) === base && l.trim() === "allOf:")
  if (allOf >= 0) {
    const items: string[][] = []
    for (const l of blockAfter(content, allOf)) {
      if (l.trimStart().startsWith("- ")) items.push([l.replace("- ", "  ")])
      else if (items.length) items[items.length - 1].push(l)
      else throw new Error(`unsupported allOf line: ${l.trim()}`)
    }
    if (items.length === 0) throw new Error("empty allOf")
    for (const item of items) {
      const part = summarize(yaml, item)
      shape.type ??= part.type
      shape.required.push(...part.required)
    }
  }
  return shape
}

/** `required: [a, b]` -> ["a", "b"]. A block-style list is not supported and throws. */
function flowList(line: string): string[] {
  const m = /^required:\s*\[([^\]]*)\]\s*$/.exec(line)
  if (!m) throw new Error(`unsupported required list (use a flow list): ${line}`)
  return m[1].split(",").map((s) => s.trim()).filter(Boolean)
}

type OpInfo = { id: string; method: string; path: string; schema: Shape | null; status: string | null }

/** Every operation of `paths:` with the shape of its first 2xx application/json response. */
function operations(yamlText: string): OpInfo[] {
  const yaml = yamlText.split("\n")
  const start = findKey(yaml, 0, "paths")
  if (start < 0) throw new Error("no paths section")
  const paths = blockAfter(yaml, start)
  const ops: OpInfo[] = []
  let path = ""
  for (let i = 0; i < paths.length; i++) {
    const line = paths[i]
    if (indentOf(line) === 2 && line.trim().startsWith("/")) path = line.trim().replace(/:$/, "")
    const m = /^ {4}(get|put|post|delete|patch|head|options|trace):$/.exec(line)
    if (!m) continue
    const block = blockAfter(paths, i)
    const idLine = block.find((l) => indentOf(l) === 6 && l.trim().startsWith("operationId:"))
    if (!idLine) throw new Error(`${m[1]} ${path} has no operationId`)
    const op: OpInfo = { id: idLine.trim().slice("operationId:".length).trim(), method: m[1], path, schema: null, status: null }
    const responses = block.findIndex((l) => indentOf(l) === 6 && l.trim() === "responses:")
    const resBlock = blockAfter(block, responses)
    for (let j = 0; j < resBlock.length; j++) {
      const code = /^ {8}'(2\d\d)':$/.exec(resBlock[j])
      if (!code) continue
      const body = blockAfter(resBlock, j)
      const content = findKey(body, 10, "content")
      if (content < 0) continue
      const contentBlock = blockAfter(body, content)
      const json = findKey(contentBlock, 12, "application/json")
      if (json < 0) continue
      const schemaBlock = blockAfter(contentBlock, json)
      const sch = findKey(schemaBlock, 14, "schema")
      if (sch < 0) throw new Error(`${op.id}: ${code[1]} has application/json without a schema`)
      op.status = code[1]
      op.schema = summarize(yaml, blockAfter(schemaBlock, sch))
      break
    }
    ops.push(op)
  }
  return ops
}

const jsonType = (v: unknown) => (Array.isArray(v) ? "array" : v === null ? "null" : typeof v)

/** Problems found when `value` is checked against `shape` (empty when it matches). */
function shapeProblems(value: unknown, shape: Shape): string[] {
  const problems: string[] = []
  const got = jsonType(value)
  if (shape.type && got !== shape.type) problems.push(`type ${got}, want ${shape.type}`)
  if (got === "object") {
    for (const key of shape.required) if (!(key in (value as object))) problems.push(`missing required key ${key}`)
  }
  return problems
}

const PW = "password1"

/** Calls every operation of the mock once, in an order that makes each succeed. */
async function callAll(svc: MockApiService): Promise<Map<string, unknown>> {
  const r = new Map<string, unknown>()
  r.set("health", await svc.health())
  r.set("healthHead", await svc.healthHead())
  r.set("register", await svc.register("ann", PW))
  r.set("login", await svc.login("ann", PW))
  r.set("me", await svc.me())
  r.set("createRepo", await svc.createRepo({ name: "r" }))
  r.set("getRepo", await svc.getRepo("ann", "r"))
  r.set("updateRepo", await svc.updateRepo("ann", "r", { description: "d" }))
  r.set("listRepos", await svc.listRepos({ owner: "ann" }))
  const first = await svc.createCommit("ann", "r", {
    branch: "main",
    expected_tip: "",
    message: "first",
    changes: [
      { path: "README.md", content: "hello\n" },
      { path: "src/a.txt", content: "a\n" },
    ],
  })
  r.set("createCommit", first)
  r.set("listCommits", await svc.listCommits("ann", "r"))
  r.set("getCommit", await svc.getCommit("ann", "r", first.oid))
  r.set("createBranch", await svc.createBranch("ann", "r", { name: "dev", from: "main" }))
  await svc.createCommit("ann", "r", {
    branch: "dev",
    expected_tip: first.oid,
    message: "second",
    changes: [{ path: "README.md", content: "hello again\n" }],
  })
  r.set("listBranches", await svc.listBranches("ann", "r"))
  r.set("compare", await svc.compare("ann", "r", "main", "dev"))
  r.set("getTreeRoot", await svc.getTreeRoot("ann", "r", "main"))
  r.set("getTree", await svc.getTree("ann", "r", "main", "src"))
  r.set("getBlob", await svc.getBlob("ann", "r", "main", "README.md"))
  // The root has no file to show: the backend answers 400 `not a file`, and the spec has no 2xx here.
  await expect(svc.getBlobRoot("ann", "r", "main")).rejects.toMatchObject({ status: 400 })
  r.set("getBlobRoot", undefined)
  r.set("mergeBranches", await svc.mergeBranches("ann", "r", { base: "main", head: "dev" }))
  r.set("deleteBranch", await svc.deleteBranch("ann", "r", "dev"))
  r.set("deleteRepo", await svc.deleteRepo("ann", "r"))
  r.set("logout", await svc.logout())
  return r
}

describe("mock service against openapi.yaml", () => {
  const ops = operations(readFileSync(specFile, "utf8"))
  let svc: MockApiService
  beforeEach(() => {
    svc = new MockApiService()
  })

  it("finds the 23 operations of the spec", () => {
    expect(ops).toHaveLength(23)
    expect(new Set(ops.map((o) => o.id)).size).toBe(23)
    const withBody = ops.filter((o) => o.schema).map((o) => o.id)
    expect(withBody).toHaveLength(17)
    for (const id of ["health", "healthHead", "logout", "deleteRepo", "deleteBranch", "getBlobRoot"]) {
      expect(withBody).not.toContain(id)
    }
  })

  it("resolves $ref, inline and allOf schemas", () => {
    const by = Object.fromEntries(ops.map((o) => [o.id, o.schema]))
    expect(by.me).toEqual({ type: "object", required: ["id", "username"] })
    expect(by.listRepos).toEqual({ type: "object", required: ["repos", "page", "per_page", "has_more"] })
    expect(by.listBranches).toEqual({ type: "object", required: ["branches"] })
    expect(by.getCommit?.type).toBe("object")
    expect(by.getCommit?.required).toEqual(["oid", "message", "author", "time", "parents", "tree", "files"])
  })

  it("has a mock method for every operationId and calls each one", async () => {
    for (const op of ops) expect(typeof (svc as unknown as Record<string, unknown>)[op.id], op.id).toBe("function")
    const results = await callAll(svc)
    expect([...results.keys()].sort()).toEqual(ops.map((o) => o.id).sort())
  })

  it("returns every required key with the documented top-level type", async () => {
    const results = await callAll(svc)
    const bad: string[] = []
    for (const op of ops) {
      if (!op.schema) continue
      for (const p of shapeProblems(results.get(op.id), op.schema)) bad.push(`${op.id} (${op.method.toUpperCase()} ${op.path}): ${p}`)
    }
    expect(bad, bad.join("; ")).toEqual([])
  })

  it("fails when a required key is removed or the type is wrong", async () => {
    const results = await callAll(svc)
    const shape = ops.find((o) => o.id === "createRepo")?.schema as Shape
    const repo = { ...(results.get("createRepo") as Record<string, unknown>) }
    expect(shapeProblems(repo, shape)).toEqual([])
    delete repo.default_branch
    expect(shapeProblems(repo, shape)).toEqual(["missing required key default_branch"])
    expect(shapeProblems([repo], shape)).toEqual(["type array, want object"])
  })

  it("keeps the scanner strict about what it cannot read", () => {
    const yaml = ["components:", "  schemas:", "    A:", "      type: object", "      required:", "        - x"]
    expect(() => summarize(yaml, componentSchema(yaml, "A"))).toThrow(/flow list/)
    expect(() => componentSchema(yaml, "Missing")).toThrow(/not found/)
    expect(() => summarize(yaml, ["$ref: '#/components/responses/X'"])).toThrow(/unsupported \$ref|not found/)
  })
})
