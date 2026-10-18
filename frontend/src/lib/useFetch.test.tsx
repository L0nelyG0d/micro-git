import { act, renderHook, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import { request as api } from "@/services/http"
import { cacheClear, invalidateAfterWrite, isImmutablePath } from "@/lib/fetchCache"
import { useFetch } from "@/lib/useFetch"

type Pending = { url: string; signal?: AbortSignal | null; resolve: (body: unknown) => void; fail: () => void }
let pending: Pending[]
let calls: string[]

const OID = "a".repeat(40)

/** fetch stub whose answers are released by hand; honours the abort signal like the real one. */
beforeEach(() => {
  cacheClear()
  pending = []
  calls = []
  vi.stubGlobal(
    "fetch",
    vi.fn(
      (url: string, init?: RequestInit) =>
        new Promise((resolve, reject) => {
          calls.push(`${init?.method ?? "GET"} ${url}`)
          const signal = init?.signal
          signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")))
          pending.push({
            url,
            signal,
            fail: () => reject(new TypeError("network")),
            resolve: (body) => resolve({ status: 200, ok: true, json: async () => body } as Response),
          })
        }),
    ),
  )
})
afterEach(() => vi.unstubAllGlobals())

const answer = (url: string, body: unknown) => {
  const p = pending.find((x) => x.url === url && !x.signal?.aborted)
  if (!p) throw new Error(`no pending request for ${url}`)
  act(() => p.resolve(body))
}

it("aborts the request on path change and never shows its answer", async () => {
  const { result, rerender } = renderHook(({ p }) => useFetch<string>(p), { initialProps: { p: "/a" } })
  const first = pending[0]
  rerender({ p: "/b" })
  expect(first.signal?.aborted).toBe(true)
  answer("/api/b", "B")
  await waitFor(() => expect(result.current.data).toBe("B"))
  expect(result.current.error).toBeUndefined()
})

it("aborts on unmount and an aborted request does not report an error", async () => {
  const { unmount } = renderHook(() => useFetch<string>("/a"))
  const signal = pending[0].signal
  unmount()
  expect(signal?.aborted).toBe(true)
  await Promise.resolve()
})

it("serves a cached path at once and revalidates in the background", async () => {
  const first = renderHook(() => useFetch<string>("/a"))
  answer("/api/a", "old")
  await waitFor(() => expect(first.result.current.data).toBe("old"))
  first.unmount()

  const second = renderHook(() => useFetch<string>("/a"))
  expect(second.result.current.data).toBe("old")
  expect(second.result.current.loading).toBe(false)
  expect(calls).toEqual(["GET /api/a", "GET /api/a"])
  answer("/api/a", "new")
  await waitFor(() => expect(second.result.current.data).toBe("new"))
})

it("never revalidates an immutable (40-hex oid) path", async () => {
  const path = `/repos/o/r/tree/${OID}`
  const first = renderHook(() => useFetch<string>(path))
  answer(`/api${path}`, "tree")
  await waitFor(() => expect(first.result.current.data).toBe("tree"))
  first.unmount()

  const second = renderHook(() => useFetch<string>(path))
  expect(second.result.current.data).toBe("tree")
  expect(calls).toHaveLength(1)

  act(() => second.result.current.reload())
  expect(calls).toHaveLength(2) // an explicit retry still goes out
})

it("fetches again when a cached oid entry is dropped while mounted", async () => {
  const path = `/repos/o/r/tree/${OID}`
  const first = renderHook(() => useFetch<string>(path))
  answer(`/api${path}`, "tree")
  await waitFor(() => expect(first.result.current.data).toBe("tree"))
  first.unmount()

  const second = renderHook(() => useFetch<string>(path))
  expect(calls).toHaveLength(1)
  act(() => invalidateAfterWrite("/repos/o/r/branches"))
  await waitFor(() => expect(calls).toHaveLength(2))
  answer(`/api${path}`, "fresh")
  await waitFor(() => expect(second.result.current.data).toBe("fresh"))
  expect(second.result.current.loading).toBe(false)
})

it("keeps showing cached data when a background revalidation fails", async () => {
  const first = renderHook(() => useFetch<string>("/a"))
  answer("/api/a", "old")
  await waitFor(() => expect(first.result.current.data).toBe("old"))
  first.unmount()

  const second = renderHook(() => useFetch<string>("/a"))
  const p = pending.find((x) => x.url === "/api/a" && !x.signal?.aborted)!
  act(() => p.fail())
  await waitFor(() => expect(second.result.current.error).toBeDefined())
  expect(second.result.current.data).toBe("old")
})

it("recognizes immutable paths only for full oids", () => {
  expect(isImmutablePath(`/repos/o/r/blob/${OID}/a.txt`)).toBe(true)
  expect(isImmutablePath(`/repos/o/r/commits?ref=${OID}&page=1`)).toBe(true)
  expect(isImmutablePath(`/repos/o/r/commits/${OID}`)).toBe(true)
  expect(isImmutablePath("/repos/o/r/tree/main")).toBe(false)
  expect(isImmutablePath("/repos/o/r/tree/abc123")).toBe(false)
})

it("drops the repo's entries after a commit, keeps other repos", async () => {
  const paths = ["/repos/o/r", "/repos/o/r/tree/main", "/repos/o/r/commits?ref=main&page=1", "/repos/o/other"]
  for (const p of paths) {
    const h = renderHook(() => useFetch<string>(p))
    answer(`/api${p}`, p)
    await waitFor(() => expect(h.result.current.data).toBe(p))
    h.unmount()
  }

  const write = api("/repos/o/r/commits", { method: "POST", body: { message: "m" } })
  answer("/api/repos/o/r/commits", {})
  await write

  for (const p of paths.slice(0, 3)) {
    const h = renderHook(() => useFetch<string>(p))
    expect(h.result.current.data).toBeUndefined()
    expect(h.result.current.loading).toBe(true)
    h.unmount()
  }
  const kept = renderHook(() => useFetch<string>("/repos/o/other"))
  expect(kept.result.current.data).toBe("/repos/o/other")
})

it("does not cache an answer that raced a write", async () => {
  const h = renderHook(() => useFetch<string>("/repos/o/r"))
  const write = api("/repos/o/r/branches", { method: "POST", body: { name: "x" } })
  answer("/api/repos/o/r/branches", {})
  await write
  answer("/api/repos/o/r", "stale") // requested before the write finished
  await waitFor(() => expect(h.result.current.data).toBe("stale"))
  h.unmount()
  const again = renderHook(() => useFetch<string>("/repos/o/r"))
  expect(again.result.current.data).toBeUndefined()
})
