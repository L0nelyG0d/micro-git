import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { ApiError, apiService } from "@/services"
import { HttpApiService } from "@/services/HttpApiService"

const svc = new HttpApiService()
let fetchMock: ReturnType<typeof vi.fn>

function respond(status: number, body?: unknown, headers: Record<string, string> = {}) {
  fetchMock.mockResolvedValueOnce({
    status,
    ok: status >= 200 && status < 300,
    headers: new Headers(headers),
    json: async () => {
      if (body === undefined) throw new SyntaxError("no body")
      return body
    },
  } as Response)
}

beforeEach(() => {
  fetchMock = vi.fn()
  vi.stubGlobal("fetch", fetchMock)
})
afterEach(() => vi.unstubAllGlobals())

type Call = { method: string; url: string; body?: unknown; credentials: string }
function lastCall(): Call {
  const [url, init] = fetchMock.mock.calls.at(-1) as [string, RequestInit]
  return {
    method: init.method as string,
    url,
    body: init.body === undefined ? undefined : JSON.parse(init.body as string),
    credentials: init.credentials as string,
  }
}

const OID = "a".repeat(40)

// One row per ApiService method (the 23 operationIds of openapi.yaml).
const cases: { name: string; run: () => Promise<unknown>; method: string; url: string; body?: unknown }[] = [
  { name: "health", run: () => svc.health(), method: "GET", url: "/api/health" },
  { name: "healthHead", run: () => svc.healthHead(), method: "HEAD", url: "/api/health" },
  { name: "register", run: () => svc.register("al", "pw"), method: "POST", url: "/api/auth/register", body: { username: "al", password: "pw" } },
  { name: "login", run: () => svc.login("al", "pw"), method: "POST", url: "/api/auth/login", body: { username: "al", password: "pw" } },
  { name: "logout", run: () => svc.logout(), method: "POST", url: "/api/auth/logout" },
  { name: "me", run: () => svc.me(), method: "GET", url: "/api/auth/me" },
  { name: "listRepos", run: () => svc.listRepos({ owner: "al", page: 2 }), method: "GET", url: "/api/repos?owner=al&page=2" },
  { name: "listRepos without query", run: () => svc.listRepos(), method: "GET", url: "/api/repos" },
  { name: "createRepo", run: () => svc.createRepo({ name: "r", description: "d", private: true }), method: "POST", url: "/api/repos", body: { name: "r", description: "d", private: true } },
  { name: "getRepo", run: () => svc.getRepo("a b", "r"), method: "GET", url: "/api/repos/a%20b/r" },
  { name: "updateRepo", run: () => svc.updateRepo("al", "r", { private: false }), method: "PATCH", url: "/api/repos/al/r", body: { private: false } },
  { name: "deleteRepo", run: () => svc.deleteRepo("al", "r"), method: "DELETE", url: "/api/repos/al/r" },
  { name: "listCommits", run: () => svc.listCommits("al", "r", { ref: "feat/x", page: 3 }), method: "GET", url: "/api/repos/al/r/commits?ref=feat%2Fx&page=3" },
  { name: "createCommit", run: () => svc.createCommit("al", "r", { branch: "main", expected_tip: OID, message: "m", changes: [{ path: "a.txt", content: "x" }] }), method: "POST", url: "/api/repos/al/r/commits", body: { branch: "main", expected_tip: OID, message: "m", changes: [{ path: "a.txt", content: "x" }] } },
  { name: "getCommit", run: () => svc.getCommit("al", "r", OID), method: "GET", url: `/api/repos/al/r/commits/${OID}` },
  { name: "compare", run: () => svc.compare("al", "r", "main", "feat/x"), method: "GET", url: "/api/repos/al/r/compare?base=main&head=feat%2Fx" },
  { name: "listBranches", run: () => svc.listBranches("al", "r"), method: "GET", url: "/api/repos/al/r/branches" },
  { name: "createBranch", run: () => svc.createBranch("al", "r", { name: "x", from: "main" }), method: "POST", url: "/api/repos/al/r/branches", body: { name: "x", from: "main" } },
  { name: "deleteBranch", run: () => svc.deleteBranch("al", "r", "feat/x"), method: "DELETE", url: "/api/repos/al/r/branches/feat%2Fx" },
  { name: "mergeBranches", run: () => svc.mergeBranches("al", "r", { base: "main", head: "x" }), method: "POST", url: "/api/repos/al/r/merge", body: { base: "main", head: "x" } },
  { name: "getTreeRoot", run: () => svc.getTreeRoot("al", "r", "main"), method: "GET", url: "/api/repos/al/r/tree/main" },
  { name: "getTree", run: () => svc.getTree("al", "r", "main", "src/a b"), method: "GET", url: "/api/repos/al/r/tree/main/src/a%20b" },
  { name: "getBlobRoot", run: () => svc.getBlobRoot("al", "r", "main"), method: "GET", url: "/api/repos/al/r/blob/main" },
  { name: "getBlob", run: () => svc.getBlob("al", "r", "main", "src/a.ts"), method: "GET", url: "/api/repos/al/r/blob/main/src/a.ts" },
  { name: "get", run: () => svc.get("/repos?page=1"), method: "GET", url: "/api/repos?page=1" },
]

describe("HttpApiService", () => {
  it.each(cases)("$name sends $method $url with the session cookie", async ({ run, method, url, body }) => {
    respond(200, { ok: true })
    await run()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const call = lastCall()
    expect(call.method).toBe(method)
    expect(call.url).toBe(url)
    expect(call.body).toEqual(body)
    expect(call.credentials).toBe("include")
  })

  it("covers every operationId of the interface", () => {
    const methods = Object.getOwnPropertyNames(HttpApiService.prototype).filter((m) => m !== "constructor" && m !== "get")
    expect(methods).toHaveLength(23)
    const tested = new Set(cases.map((c) => c.name.split(" ")[0]))
    for (const m of methods) expect(tested, m).toContain(m)
  })

  it("sends JSON bodies with a Content-Type header and none without a body", async () => {
    respond(200, {})
    await svc.login("al", "pw")
    expect((fetchMock.mock.calls[0][1] as RequestInit).headers).toEqual({ "Content-Type": "application/json" })
    respond(200, {})
    await svc.me()
    expect((fetchMock.mock.calls[1][1] as RequestInit).headers).toBeUndefined()
  })

  it("returns the parsed body and undefined for 204", async () => {
    respond(200, { id: 1, username: "al" })
    expect(await svc.me()).toEqual({ id: 1, username: "al" })
    respond(204)
    expect(await svc.logout()).toBeUndefined()
  })

  it("throws ApiError with the server's error message and Retry-After", async () => {
    respond(429, { error: "slow down" }, { "Retry-After": "7" })
    const err = await svc.login("al", "pw").catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ status: 429, message: "slow down", retryAfter: 7 })
  })

  it("falls back to a generic message when the error body is not usable", async () => {
    respond(500)
    const err = await svc.me().catch((e) => e)
    expect(err).toMatchObject({ status: 500, message: "Something went wrong. Try again." })
    expect(err.retryAfter).toBeUndefined()
  })

  it("passes network failures through unchanged", async () => {
    const boom = new TypeError("network")
    fetchMock.mockRejectedValueOnce(boom)
    await expect(svc.me()).rejects.toBe(boom)
  })

  it("forwards the abort signal on reads", async () => {
    const ctl = new AbortController()
    respond(200, {})
    await svc.getRepo("al", "r", { signal: ctl.signal })
    expect((fetchMock.mock.calls[0][1] as RequestInit).signal).toBe(ctl.signal)
  })
})

describe("apiService", () => {
  it("is the HTTP implementation behind the ApiService type", () => {
    expect(apiService).toBeInstanceOf(HttpApiService)
  })
})
