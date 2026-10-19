// Live parity check (#113): the same journey through MockApiService and the real
// HttpApiService against a running backend; the JSON shapes (keys and value types)
// of every answer must match. Skipped unless SMOKE_BASE_URL is set (the root
// scripts/smoke-sqlite.sh sets it to the Vite dev server, so /api goes through the proxy).
import { afterAll, beforeAll, describe, expect, it, vi } from "vitest"
import { ApiError } from "@/services"
import type { ApiService } from "@/services"
import { HttpApiService } from "@/services/HttpApiService"
import { MockApiService } from "@/services/MockApiService"

const BASE = process.env.SMOKE_BASE_URL

/** Recursive shape: object keys sorted, arrays by their first element, scalars by typeof. */
function shape(v: unknown): unknown {
  if (v === null) return "null"
  if (Array.isArray(v)) return v.length ? [shape(v[0])] : []
  if (typeof v === "object") return Object.fromEntries(Object.entries(v as object).sort(([a], [b]) => a.localeCompare(b)).map(([k, x]) => [k, shape(x)]))
  return typeof v
}

async function attempt(p: Promise<unknown>) {
  try {
    return { ok: shape(await p) }
  } catch (e) {
    if (e instanceof ApiError) return { error: e.status, message: e.message, hasTip: e.currentTip !== undefined }
    throw e
  }
}

/** The journey; every step's outcome is recorded so the two services can be compared. */
async function journey(svc: ApiService, name: string) {
  const out: Record<string, unknown> = {}
  const step = async (k: string, p: Promise<unknown>) => (out[k] = await attempt(p))
  await step("me-anon", svc.me())
  await step("register", svc.register(name, "parity-pass-1"))
  await step("register-dup", svc.register(name, "parity-pass-1"))
  await step("login-bad", svc.login(name, "wrong-password"))
  await step("login", svc.login(name, "parity-pass-1"))
  await step("me", svc.me())
  await step("createRepo", svc.createRepo({ name: "demo", description: "d" }))
  await step("createRepo-dup", svc.createRepo({ name: "demo" }))
  await step("history-empty", svc.listCommits(name, "demo"))
  const c1 = await svc.createCommit(name, "demo", {
    branch: "main", expected_tip: "", message: "first",
    changes: [{ path: "README.md", content: "hi\n" }, { path: "src/a.txt", content: "a\n" }],
  })
  out.commit1 = shape(c1)
  const c2 = await svc.createCommit(name, "demo", {
    branch: "main", expected_tip: c1.oid, message: "second", changes: [{ path: "README.md", content: "hello\n" }],
  })
  await step("commit-stale", svc.createCommit(name, "demo", { branch: "main", expected_tip: c1.oid, message: "x", changes: [{ path: "z", content: "z" }] }))
  await step("listRepos", svc.listRepos())
  await step("getRepo", svc.getRepo(name, "demo"))
  await step("listCommits", svc.listCommits(name, "demo"))
  await step("getCommit", svc.getCommit(name, "demo", c2.oid))
  await step("compare", svc.compare(name, "demo", c1.oid, c2.oid))
  await step("tree-root", svc.getTreeRoot(name, "demo", "main"))
  await step("tree-sub", svc.getTree(name, "demo", "main", "src"))
  await step("blob", svc.getBlob(name, "demo", "main", "README.md"))
  await step("blob-missing", svc.getBlob(name, "demo", "main", "nope.txt"))
  await step("createBranch", svc.createBranch(name, "demo", { name: "feat", from: "main" }))
  await step("listBranches", svc.listBranches(name, "demo"))
  await step("updateRepo", svc.updateRepo(name, "demo", { description: "new" }))
  await step("logout", svc.logout())
  await step("me-after-logout", svc.me())
  return out
}

describe.skipIf(!BASE)("mock and real service give the same shapes (live backend)", () => {
  beforeAll(() => {
    // Node fetch needs absolute URLs and has no cookie jar: add both, like a browser.
    const jar = new Map<string, string>()
    vi.stubGlobal("fetch", async (url: string, init: RequestInit = {}) => {
      const headers = new Headers(init.headers)
      if (jar.size) headers.set("Cookie", [...jar].map(([k, v]) => `${k}=${v}`).join("; "))
      const res = await fetch_(BASE + url, { ...init, headers })
      for (const c of res.headers.getSetCookie()) {
        const [pair] = c.split(";")
        const i = pair.indexOf("=")
        const [k, v] = [pair.slice(0, i), pair.slice(i + 1)]
        if (v === "" || /Max-Age=0/i.test(c)) jar.delete(k)
        else jar.set(k, v)
      }
      return res
    })
  })
  afterAll(() => vi.unstubAllGlobals())

  it("runs the same journey on both", async () => {
    const real = await journey(new HttpApiService(), "parity")
    const mock = await journey(new MockApiService(), "parity")
    expect(real).toEqual(mock)
  })
})

const fetch_ = globalThis.fetch
