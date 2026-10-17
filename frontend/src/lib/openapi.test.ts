import { readdirSync, readFileSync, statSync } from "node:fs"
import { join, resolve } from "node:path"
import { describe, expect, it } from "vitest"

// Every API path the frontend builds must exist in the OpenAPI contract
// (openapi.yaml at the repo root). Paths are found as string/template literals
// that start with /repos, /auth or /health, or with the local `base` /
// `apiBase` variables (the repository path) followed by a sub-path.

// Vitest runs from frontend/.
const srcDir = resolve(process.cwd(), "src") + "/"
const specFile = resolve(process.cwd(), "../openapi.yaml")

/** Values of the template variable `${kind}` in lib/git.ts `readPath`. */
const KIND_VALUES = ["tree", "blob"]

/** Spec paths from the `paths:` section, with `{x}` kept as `{param}`. */
function specPaths(yaml: string): string[] {
  const paths: string[] = []
  let inPaths = false
  for (const line of yaml.split("\n")) {
    if (line !== "" && !line.startsWith(" ") && !line.startsWith("#")) {
      inPaths = line.startsWith("paths:")
      continue
    }
    if (inPaths && line.startsWith("  /")) paths.push(line.trim().replace(/:$/, ""))
  }
  return paths
}

/** Turns a spec or client path into segments, every `{...}` becoming `{}`. */
function segments(path: string): string[] {
  return path.split("/").map((s) => (s.includes("{") ? "{}" : s))
}

/** API paths found in the source of one file, normalised (`${..}` -> `{param}`). */
function clientPaths(source: string): string[] {
  const found: string[] = []
  for (const m of source.matchAll(/`([^`]*)`|"([^"\n]*)"/g)) {
    let lit = m[1] ?? m[2]
    if (/^\$\{(base|apiBase)\}\/[a-z]/.test(lit)) {
      lit = lit.replace(/^\$\{(base|apiBase)\}/, "/repos/${owner}/${repo}")
    } else if (!/^\/(repos|auth|health)(\/|\?|$)/.test(lit)) {
      continue
    }
    lit = lit.split("?")[0]
    const variants = lit.includes("${kind}") ? KIND_VALUES.map((k) => lit.replace("${kind}", k)) : [lit]
    for (const v of variants) found.push(v.replace(/\$\{[^}]*\}/g, "{param}"))
  }
  return found
}

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const full = join(dir, name)
    if (statSync(full).isDirectory()) return sourceFiles(full)
    return /\.(ts|tsx)$/.test(name) && !/\.test\.(ts|tsx)$/.test(name) ? [full] : []
  })
}

function inSpec(spec: string[], path: string): boolean {
  const want = segments(path)
  return spec.some((p) => {
    const have = segments(p)
    return have.length === want.length && have.every((s, i) => s === want[i])
  })
}

describe("openapi.yaml", () => {
  const spec = specPaths(readFileSync(specFile, "utf8"))

  it("lists the paths section", () => {
    expect(spec).toContain("/repos")
    expect(spec).toContain("/repos/{owner}/{repo}/tree/{ref}/{path}")
  })

  it("contains every path the frontend requests", () => {
    const missing: string[] = []
    let total = 0
    for (const file of sourceFiles(srcDir)) {
      for (const path of clientPaths(readFileSync(file, "utf8"))) {
        total++
        if (!inSpec(spec, path)) missing.push(`${path} (in ${file.slice(srcDir.length)})`)
      }
    }
    expect(total).toBeGreaterThan(10)
    expect(missing, `paths missing from openapi.yaml: ${missing.join(", ")}`).toEqual([])
  })

  it("reports a path that is not in the spec", () => {
    const [bad] = clientPaths("api(`/repos/${o}/${r}/stars`)")
    expect(bad).toBe("/repos/{param}/{param}/stars")
    expect(inSpec(spec, bad)).toBe(false)
    expect(inSpec(spec, "/auth/me")).toBe(true)
  })

  it("normalises base-relative and kind paths", () => {
    expect(clientPaths("useFetch(`${base}/commits?ref=${r}`)")).toEqual(["/repos/{param}/{param}/commits"])
    expect(clientPaths("`/repos/${a}/${b}/${kind}/${c}`")).toEqual([
      "/repos/{param}/{param}/tree/{param}",
      "/repos/{param}/{param}/blob/{param}",
    ])
  })
})
