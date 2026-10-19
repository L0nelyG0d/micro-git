import { beforeEach, describe, expect, it } from "vitest"
import { ApiError } from "./http"
import { MockApiService } from "./MockApiService"

let svc: MockApiService
beforeEach(() => {
  svc = new MockApiService()
})

/** Status of the ApiError a promise rejects with. */
async function status(p: Promise<unknown>): Promise<number | string> {
  try {
    await p
    return "ok"
  } catch (e) {
    if (e instanceof ApiError) return e.status
    throw e
  }
}
async function message(p: Promise<unknown>): Promise<string> {
  try {
    await p
  } catch (e) {
    return (e as Error).message
  }
  return "no error"
}

const PW = "password1"
/** Register and log in: register alone starts no session. */
async function signUp(name: string) {
  const u = await svc.register(name, PW)
  await svc.login(name, PW)
  return u
}
/** Registers `name`, creates repo `r` and makes a first commit; returns the commit oid. */
async function seeded(name = "ann", repo = "r", files = [{ path: "README.md", content: "hello\n" }]) {
  await signUp(name)
  await svc.createRepo({ name: repo })
  const { oid } = await svc.createCommit(name, repo, { branch: "main", expected_tip: "", message: "first", changes: files })
  return oid
}

describe("auth", () => {
  it("login sets the user, register does not, logout clears it, me needs a session", async () => {
    expect(await status(svc.me())).toBe(401)
    const u = await svc.register("Ann", PW)
    expect(u.username).toBe("ann")
    // Like the backend, register starts no session: log in next.
    expect(await status(svc.me())).toBe(401)
    expect((await svc.login("ANN", PW)).id).toBe(u.id)
    expect(await svc.me()).toEqual(u)
    await svc.logout()
    expect(await status(svc.me())).toBe(401)
    await svc.login("ann", PW)
    expect(await svc.me()).toEqual(u)
  })

  it("rejects bad credentials with ApiError", async () => {
    await svc.register("ann", PW)
    await svc.logout()
    expect(await status(svc.login("ann", "wrong-pass"))).toBe(401)
    expect(await status(svc.login("nobody", PW))).toBe(401)
    expect(await status(svc.login("", ""))).toBe(400)
    expect(await status(svc.register("a", PW))).toBe(400)
    expect(await status(svc.register("api", PW))).toBe(400)
    expect(await status(svc.register("bob", "short"))).toBe(400)
  })

  it("usernames are unique case-insensitively", async () => {
    await svc.register("ann", PW)
    expect(await status(svc.register("ANN", PW))).toBe(409)
  })

  it("logout works without a session", async () => {
    await expect(svc.logout()).resolves.toBeUndefined()
  })
})

describe("repositories", () => {
  it("creates, lists, updates and deletes", async () => {
    await signUp("ann")
    const repo = await svc.createRepo({ name: "demo", description: "d" })
    expect(repo).toMatchObject({ owner: "ann", name: "demo", description: "d", default_branch: "main", private: false, empty: true })
    expect((await svc.listRepos()).repos.map((r) => r.name)).toEqual(["demo"])
    const up = await svc.updateRepo("ann", "demo", { name: "renamed", description: "x", private: true })
    expect(up).toMatchObject({ name: "renamed", description: "x", private: true })
    expect(await status(svc.getRepo("ann", "demo"))).toBe(404)
    await svc.deleteRepo("ann", "renamed")
    expect((await svc.listRepos()).repos).toEqual([])
  })

  it("repo names are unique per owner, case-insensitively, and may repeat across owners", async () => {
    await signUp("ann")
    await svc.createRepo({ name: "Demo" })
    expect(await status(svc.createRepo({ name: "demo" }))).toBe(409)
    await svc.createRepo({ name: "other" })
    expect(await status(svc.updateRepo("ann", "other", { name: "DEMO" }))).toBe(409)
    await signUp("bob")
    await expect(svc.createRepo({ name: "demo" })).resolves.toMatchObject({ owner: "bob" })
  })

  it("validates names and description", async () => {
    await signUp("ann")
    for (const name of ["", ".", "..", "a b", "x".repeat(101)]) expect(await status(svc.createRepo({ name }))).toBe(400)
    expect(await status(svc.createRepo({ name: "ok", description: "x".repeat(501) }))).toBe(400)
    await svc.createRepo({ name: "ok" })
    expect(await status(svc.updateRepo("ann", "ok", {}))).toBe(400)
  })

  it("matches owner and repo case-insensitively", async () => {
    await signUp("ann")
    await svc.createRepo({ name: "Demo" })
    expect((await svc.getRepo("ANN", "demo")).name).toBe("Demo")
  })

  it("paginates the list", async () => {
    await signUp("ann")
    for (const n of ["a", "b", "c"]) await svc.createRepo({ name: n })
    const p1 = await svc.listRepos({ per_page: 2 })
    expect(p1).toMatchObject({ page: 1, per_page: 2, has_more: true })
    expect(p1.repos.map((r) => r.name)).toEqual(["c", "b"])
    expect((await svc.listRepos({ per_page: 2, page: 2 })).repos.map((r) => r.name)).toEqual(["a"])
    expect(await status(svc.listRepos({ per_page: 101 }))).toBe(400)
    expect(await status(svc.listRepos({ page: 0 }))).toBe(400)
  })
})

describe("write access", () => {
  it("answers 401 without a session and 403 for a non-owner", async () => {
    await seeded()
    await svc.logout()
    const input = { branch: "main", expected_tip: "", message: "m", changes: [{ path: "x", content: "y" }] }
    expect(await status(svc.createRepo({ name: "n" }))).toBe(401)
    expect(await status(svc.updateRepo("ann", "r", { description: "x" }))).toBe(401)
    expect(await status(svc.deleteRepo("ann", "r"))).toBe(401)
    expect(await status(svc.createCommit("ann", "r", input))).toBe(401)
    expect(await status(svc.createBranch("ann", "r", { name: "b", from: "main" }))).toBe(401)
    expect(await status(svc.deleteBranch("ann", "r", "main"))).toBe(401)
    expect(await status(svc.mergeBranches("ann", "r", { base: "main", head: "b" }))).toBe(401)

    await signUp("bob")
    expect(await status(svc.updateRepo("ann", "r", { description: "x" }))).toBe(403)
    expect(await status(svc.deleteRepo("ann", "r"))).toBe(403)
    expect(await status(svc.createCommit("ann", "r", input))).toBe(403)
    expect(await status(svc.createBranch("ann", "r", { name: "b", from: "main" }))).toBe(403)
    expect(await status(svc.deleteBranch("ann", "r", "main"))).toBe(403)
    expect(await status(svc.mergeBranches("ann", "r", { base: "main", head: "main" }))).toBe(403)
    // a missing repo or branch is 404 before 403
    expect(await status(svc.deleteRepo("ann", "nope"))).toBe(404)
    expect(await status(svc.createBranch("ann", "r", { name: "b", from: "nope" }))).toBe(404)
  })
})

describe("visibility", () => {
  it("hides a private repo from non-owners on every read, but not from the owner", async () => {
    const oid = await seeded("ann", "secret")
    await svc.updateRepo("ann", "secret", { private: true })
    expect((await svc.listRepos()).repos).toHaveLength(1)
    const reads = (): Promise<unknown>[] => [
      svc.getRepo("ann", "secret"),
      svc.listCommits("ann", "secret"),
      svc.getCommit("ann", "secret", oid),
      svc.compare("ann", "secret", "main", "main"),
      svc.listBranches("ann", "secret"),
      svc.getTreeRoot("ann", "secret", "main"),
      svc.getTree("ann", "secret", "main", ""),
      svc.getBlobRoot("ann", "secret", "main"),
      svc.getBlob("ann", "secret", "main", "README.md"),
      svc.get("/repos/ann/secret"),
    ]
    const ownerStatuses = await Promise.all(reads().map(status))
    expect(ownerStatuses).toEqual(["ok", "ok", "ok", "ok", "ok", "ok", "ok", 400, "ok", "ok"])

    for (const who of ["bob", null]) {
      await svc.logout()
      if (who) await signUp(who)
      expect(await Promise.all(reads().map(status))).toEqual(Array(10).fill(404))
      expect((await svc.listRepos()).repos).toEqual([])
      expect((await svc.listRepos({ owner: "ann" })).repos).toEqual([])
    }
    await svc.login("ann", PW)
    await svc.updateRepo("ann", "secret", { private: false })
    await svc.logout()
    expect((await svc.listRepos()).repos).toHaveLength(1)
  })

  it("createRepo accepts private", async () => {
    await signUp("ann")
    expect((await svc.createRepo({ name: "p", private: true })).private).toBe(true)
  })
})

describe("branches", () => {
  it("validates branch names like validBranchName", async () => {
    const oid = await seeded()
    const bad = ["", ".", "..", "a b", "a/b", "x".repeat(101), oid]
    for (const name of bad) {
      expect(await status(svc.createBranch("ann", "r", { name, from: "main" }))).toBe(400)
      expect(await status(svc.createCommit("ann", "r", { branch: name, expected_tip: oid, message: "m", changes: [{ path: "a", content: "b" }] }))).toBe(400)
    }
    for (const name of ["a.b_c-d", "x".repeat(100), "1"]) {
      expect((await svc.createBranch("ann", "r", { name, from: "main" })).oid).toBe(oid)
    }
  })

  it("creates, lists and deletes; the default branch stays", async () => {
    const oid = await seeded()
    expect(await svc.createBranch("ann", "r", { name: "dev", from: "main" })).toEqual({ name: "dev", oid, is_default: false })
    expect(await status(svc.createBranch("ann", "r", { name: "dev", from: "main" }))).toBe(409)
    expect((await svc.listBranches("ann", "r")).branches).toEqual([
      { name: "dev", oid, is_default: false },
      { name: "main", oid, is_default: true },
    ])
    expect(await status(svc.deleteBranch("ann", "r", "main"))).toBe(400)
    expect(await status(svc.deleteBranch("ann", "r", "nope"))).toBe(404)
    await svc.deleteBranch("ann", "r", "dev")
    expect((await svc.listBranches("ann", "r")).branches).toHaveLength(1)
  })

  it("setting the default branch needs an existing branch", async () => {
    await seeded()
    expect(await status(svc.updateRepo("ann", "r", { default_branch: "nope" }))).toBe(404)
    await svc.createBranch("ann", "r", { name: "dev", from: "main" })
    expect((await svc.updateRepo("ann", "r", { default_branch: "dev" })).default_branch).toBe("dev")
  })
})

describe("commits", () => {
  it("first commit goes to the default branch with an empty expected_tip", async () => {
    await signUp("ann")
    await svc.createRepo({ name: "r" })
    const input = { branch: "dev", expected_tip: "", message: "m", changes: [{ path: "a", content: "b" }] }
    expect(await status(svc.createCommit("ann", "r", input))).toBe(400)
    const { oid } = await svc.createCommit("ann", "r", { ...input, branch: "main" })
    expect((await svc.getRepo("ann", "r")).empty).toBe(false)
    expect(oid).toMatch(/^[0-9a-f]{40}$/)
  })

  it("a stale expected_tip gives 409 and changes nothing", async () => {
    const first = await seeded()
    const second = (await svc.createCommit("ann", "r", { branch: "main", expected_tip: first, message: "two", changes: [{ path: "a", content: "1" }] })).oid
    const stale = { branch: "main", expected_tip: first, message: "late", changes: [{ path: "b", content: "2" }] }
    expect(await status(svc.createCommit("ann", "r", stale))).toBe(409)
    expect(await status(svc.createCommit("ann", "r", { ...stale, expected_tip: "" }))).toBe(409)
    // The 409 carries the branch's current tip (openapi TipMoved, backend writeTipMoved).
    const err = await svc.createCommit("ann", "r", stale).catch((e) => e)
    expect(err).toMatchObject({ status: 409, message: "branch tip has moved", currentTip: second })
    expect((await svc.listBranches("ann", "r")).branches[0].oid).toBe(second)
    expect(await status(svc.createCommit("ann", "r", { ...stale, expected_tip: "zz" }))).toBe(400)
  })

  it("validates message and changes", async () => {
    const tip = await seeded()
    const base = { branch: "main", expected_tip: tip, message: "m", changes: [{ path: "a", content: "b" }] }
    expect(await status(svc.createCommit("ann", "r", { ...base, message: "  " }))).toBe(400)
    expect(await status(svc.createCommit("ann", "r", { ...base, message: "x".repeat(501) }))).toBe(400)
    expect(await status(svc.createCommit("ann", "r", { ...base, changes: [] }))).toBe(400)
    expect(await message(svc.createCommit("ann", "r", { ...base, changes: [{ path: "README.md", content: "hello\n" }] }))).toBe("no changes")
    expect(await status(svc.createCommit("ann", "r", { ...base, branch: "nope" }))).toBe(404)
  })

  it("answers 400, not a TypeError, for a change entry that is not an object", async () => {
    const tip = await seeded()
    for (const bad of [null, 5, "x"]) {
      const input = { branch: "main", expected_tip: tip, message: "m", changes: [bad] } as unknown as Parameters<typeof svc.createCommit>[2]
      expect(await status(svc.createCommit("ann", "r", input))).toBe(400)
    }
  })

  it("lists tree entries in UTF-8 byte order", async () => {
    await seeded("ann", "r", [{ path: "\u{1F600}", content: "a" }, { path: "\uFF5E", content: "b" }])
    expect((await svc.getTreeRoot("ann", "r", "main")).entries.map((e) => e.name)).toEqual(["\uFF5E", "\u{1F600}"])
  })

  it("history is computed from the stored commits: parents, order, paging", async () => {
    const c1 = await seeded()
    const c2 = (await svc.createCommit("ann", "r", { branch: "main", expected_tip: c1, message: "second", changes: [{ path: "a.txt", content: "x\n" }] })).oid
    const c3 = (await svc.createCommit("ann", "r", { branch: "main", expected_tip: c2, message: "third", changes: [{ path: "a.txt", content: "y\n" }] })).oid
    const all = await svc.listCommits("ann", "r")
    expect(all.commits.map((c) => [c.oid, c.message, c.author, c.parents])).toEqual([
      [c3, "third", "ann", [c2]],
      [c2, "second", "ann", [c1]],
      [c1, "first", "ann", []],
    ])
    expect(all.commits[0].time).toMatch(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/)
    const p2 = await svc.listCommits("ann", "r", { per_page: 2, page: 2 })
    expect(p2.commits.map((c) => c.oid)).toEqual([c1])
    expect(p2.has_more).toBe(false)
    expect((await svc.listCommits("ann", "r", { per_page: 2 })).has_more).toBe(true)
    expect((await svc.listCommits("ann", "r", { ref: c2 })).commits).toHaveLength(2)
    expect(await status(svc.listCommits("ann", "r", { ref: "nope" }))).toBe(404)
    expect(await status(svc.listCommits("ann", "r", { page: 101, per_page: 100 }))).toBe(400)
  })

  it("getCommit diffs against the first parent", async () => {
    const c1 = await seeded()
    const c2 = (await svc.createCommit("ann", "r", { branch: "main", expected_tip: c1, message: "m", changes: [{ path: "README.md", content: "hello\nworld\n" }, { path: "n.txt", content: "n\n" }] })).oid
    const root = await svc.getCommit("ann", "r", c1)
    expect(root.files.map((f) => [f.path, f.status, f.additions])).toEqual([["README.md", "added", 1]])
    const d = await svc.getCommit("ann", "r", c2)
    expect(d.files.map((f) => [f.path, f.status])).toEqual([["README.md", "modified"], ["n.txt", "added"]])
    expect(d.files[0].hunks[0].lines).toEqual([
      { type: "context", text: "hello" },
      { type: "add", text: "world" },
    ])
    expect(d.parents).toEqual([c1])
    expect(await status(svc.getCommit("ann", "r", "abc"))).toBe(400)
    expect(await status(svc.getCommit("ann", "r", "0".repeat(40)))).toBe(404)
  })

  it("compare diffs two tips", async () => {
    const c1 = await seeded()
    await svc.createBranch("ann", "r", { name: "dev", from: "main" })
    await svc.createCommit("ann", "r", { branch: "dev", expected_tip: c1, message: "m", changes: [{ path: "README.md", delete: true }] })
    const cmp = await svc.compare("ann", "r", "main", "dev")
    expect(cmp).toMatchObject({ base: "main", head: "dev", base_commit: c1 })
    expect(cmp.files.map((f) => [f.path, f.status, f.deletions])).toEqual([["README.md", "deleted", 1]])
    expect(await status(svc.compare("ann", "r", "", "dev"))).toBe(400)
    expect(await status(svc.compare("ann", "r", "main", "nope"))).toBe(404)
  })
})

describe("merge", () => {
  async function twoBranches() {
    const c1 = await seeded()
    await svc.createBranch("ann", "r", { name: "dev", from: "main" })
    return c1
  }

  it("fast-forwards the base to the head", async () => {
    const c1 = await twoBranches()
    const c2 = (await svc.createCommit("ann", "r", { branch: "dev", expected_tip: c1, message: "m", changes: [{ path: "a", content: "b" }] })).oid
    expect(await svc.mergeBranches("ann", "r", { base: "main", head: "dev" })).toEqual({ branch: "main", oid: c2, previous_oid: c1 })
    expect((await svc.listCommits("ann", "r")).commits[0].oid).toBe(c2)
    expect(await message(svc.mergeBranches("ann", "r", { base: "main", head: "dev" }))).toBe("already up to date")
  })

  it("refuses a non-fast-forward with 409", async () => {
    const c1 = await twoBranches()
    await svc.createCommit("ann", "r", { branch: "dev", expected_tip: c1, message: "d", changes: [{ path: "a", content: "1" }] })
    await svc.createCommit("ann", "r", { branch: "main", expected_tip: c1, message: "m", changes: [{ path: "b", content: "2" }] })
    const err = svc.mergeBranches("ann", "r", { base: "main", head: "dev" })
    expect(await status(err)).toBe(409)
    expect(await message(svc.mergeBranches("ann", "r", { base: "main", head: "dev" }))).toBe("not a fast-forward")
  })

  it("answers 409 branch tip has moved when the base moves before the swap, and changes nothing", async () => {
    const c1 = await twoBranches()
    const c2 = (await svc.createCommit("ann", "r", { branch: "dev", expected_tip: c1, message: "m", changes: [{ path: "a", content: "b" }] })).oid
    svc.beforeMergeSwap = async () => {
      await svc.createCommit("ann", "r", { branch: "main", expected_tip: c1, message: "race", changes: [{ path: "c", content: "d" }] })
    }
    const raced = await svc.mergeBranches("ann", "r", { base: "main", head: "dev" }).catch((e) => e)
    const moved = (await svc.listBranches("ann", "r")).branches.find((b) => b.name === "main")!.oid
    expect(raced).toMatchObject({ status: 409, message: "branch tip has moved", currentTip: moved })
    expect(moved).not.toBe(c1)
    svc.beforeMergeSwap = async () => {}
    expect(await message(svc.mergeBranches("ann", "r", { base: "main", head: "dev" }))).toBe("not a fast-forward")
    expect((await svc.listBranches("ann", "r")).branches.find((b) => b.name === "dev")!.oid).toBe(c2)
  })

  it("rejects bad input", async () => {
    await twoBranches()
    expect(await status(svc.mergeBranches("ann", "r", { base: "main", head: "main" }))).toBe(400)
    expect(await status(svc.mergeBranches("ann", "r", { base: "main", head: "nope" }))).toBe(404)
    expect(await status(svc.mergeBranches("ann", "r", { base: "a b", head: "dev" }))).toBe(400)
  })
})

describe("tree and blob", () => {
  it("reads directories (dirs first) and files at a ref or commit oid", async () => {
    const oid = await seeded("ann", "r", [
      { path: "z.txt", content: "z\n" },
      { path: "src/a.ts", content: "const a = 1\n" },
      { path: "src/lib/b.ts", content: "b\n" },
    ])
    const root = await svc.getTreeRoot("ann", "r", "main")
    expect(root).toMatchObject({ ref: "main", commit: oid, path: "" })
    expect(root.entries.map((e) => [e.name, e.type])).toEqual([["src", "tree"], ["z.txt", "blob"]])
    const src = await svc.getTree("ann", "r", oid, "src")
    expect(src.entries.map((e) => e.name)).toEqual(["lib", "a.ts"])
    const blob = await svc.getBlob("ann", "r", "main", "src/a.ts")
    expect(blob).toMatchObject({ name: "a.ts", path: "src/a.ts", content: "const a = 1\n", size: 12 })
    expect(await svc.get("/repos/ann/r/blob/main/src/a.ts")).toEqual(blob)
    expect(await svc.get("/repos/ann/r/tree/main/src/")).toMatchObject({ path: "src" })
  })

  it("blob oids match git: `hello\\n` is ce013625...", async () => {
    await seeded()
    expect((await svc.getBlob("ann", "r", "main", "README.md")).oid).toBe("ce013625030ba8dba906f756967f9e9ca394464a")
  })

  it("answers the documented errors", async () => {
    await seeded("ann", "r", [{ path: "src/a.ts", content: "a\n" }])
    expect(await status(svc.getTree("ann", "r", "nope", ""))).toBe(404)
    expect(await status(svc.getTree("ann", "r", "main", "missing"))).toBe(404)
    expect(await status(svc.getTree("ann", "r", "main", "src/a.ts/deeper"))).toBe(404)
    expect(await status(svc.getTree("ann", "r", "main", "src/a.ts"))).toBe(400)
    expect(await status(svc.getBlob("ann", "r", "main", "src"))).toBe(400)
    expect(await status(svc.getBlobRoot("ann", "r", "main"))).toBe(400)
    expect(await status(svc.getTree("ann", "r", "main", "a/../b"))).toBe(400)
  })

  it("a branch created from a commit sees later edits only on its own branch", async () => {
    const c1 = await seeded()
    await svc.createBranch("ann", "r", { name: "dev", from: "main" })
    await svc.createCommit("ann", "r", { branch: "dev", expected_tip: c1, message: "m", changes: [{ path: "README.md", content: "changed\n" }] })
    expect((await svc.getBlob("ann", "r", "main", "README.md")).content).toBe("hello\n")
    expect((await svc.getBlob("ann", "r", "dev", "README.md")).content).toBe("changed\n")
  })
})

describe("get()", () => {
  it("serves the path-addressed reads the UI uses", async () => {
    await seeded()
    expect(await svc.get("/auth/me")).toMatchObject({ username: "ann" })
    expect(((await svc.get("/repos?owner=ann&per_page=5")) as { repos: unknown[] }).repos).toHaveLength(1)
    expect(await svc.get("/repos/ann/r/branches")).toMatchObject({ branches: [{ name: "main" }] })
    expect(await svc.get("/repos/ann/r/commits?ref=main&page=1")).toMatchObject({ page: 1 })
    expect(await svc.get("/repos/ann/r/compare?base=main&head=main")).toMatchObject({ files: [] })
    expect(await status(svc.get("/nope"))).toBe(404)
    expect(await status(svc.get("/repos?page=x"))).toBe(400)
  })

  it("rejects when the signal is already aborted", async () => {
    const ac = new AbortController()
    ac.abort()
    await expect(svc.get("/repos", { signal: ac.signal })).rejects.toMatchObject({ name: "AbortError" })
  })
})
