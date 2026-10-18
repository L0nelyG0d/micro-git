import { describe, expect, it } from "vitest"
import {
  applyChanges,
  compareBytes,
  diffText,
  diffTrees,
  encodeText,
  flattenTree,
  hashObject,
  parseCommit,
  parseTree,
  putObject,
  serializeCommit,
  serializeTree,
  type ObjectStore,
} from "./mockGit"

describe("git objects", () => {
  it("hashes the blob `hello\\n` like real git", async () => {
    expect(await hashObject("blob", encodeText("hello\n"))).toBe("ce013625030ba8dba906f756967f9e9ca394464a")
  })

  it("hashes the empty tree like real git", async () => {
    expect(await hashObject("tree", new Uint8Array())).toBe("4b825dc642cb6eb9a060e54bf8d69288fbee4904")
  })

  it("round-trips trees in git order (file foo.txt before directory foo)", () => {
    const oid = "a".repeat(40)
    const body = serializeTree([
      { mode: "40000", name: "foo", oid },
      { mode: "100644", name: "foo.txt", oid },
    ])
    expect(parseTree(body).map((e) => e.name)).toEqual(["foo.txt", "foo"])
  })

  it("sorts by UTF-8 bytes, not UTF-16 code units", async () => {
    // U+FF5E is EF BD 9E in UTF-8 but FF5E in UTF-16; U+1F600 is F0 9F 98 80 / D83D DE00.
    const wide = "\uFF5E"
    const emoji = "\u{1F600}"
    expect(wide > emoji).toBe(true) // UTF-16 order
    expect(compareBytes(wide, emoji)).toBeLessThan(0) // byte order
    const oid = "a".repeat(40)
    const body = serializeTree([
      { mode: "100644", name: emoji, oid },
      { mode: "100644", name: wide, oid },
    ])
    expect(parseTree(body).map((e) => e.name)).toEqual([wide, emoji])
  })

  it("round-trips commits", () => {
    const sig = { name: "ann", email: "ann@micro-git.local", unix: 1700000000 }
    const c = { tree: "b".repeat(40), parents: ["c".repeat(40)], author: sig, committer: sig, message: "  hi  " }
    expect(parseCommit(serializeCommit(c))).toEqual({ ...c, message: "hi\n" })
  })
})

describe("applyChanges", () => {
  async function setup() {
    const store: ObjectStore = new Map()
    const tree = await applyChanges(store, "", [
      { path: "a.txt", content: "one\n" },
      { path: "dir/b.txt", content: "two\n" },
    ])
    return { store, tree }
  }

  it("builds nested trees", async () => {
    const { store, tree } = await setup()
    expect([...flattenTree(store, tree).keys()].sort()).toEqual(["a.txt", "dir/b.txt"])
  })

  it("is deterministic: same files, same root oid", async () => {
    const a = await setup()
    const b = await setup()
    expect(a.tree).toBe(b.tree)
  })

  it("deletes a file and drops the emptied directory", async () => {
    const { store, tree } = await setup()
    const next = await applyChanges(store, tree, [{ path: "dir/b.txt", delete: true }])
    expect([...flattenTree(store, next).keys()]).toEqual(["a.txt"])
  })

  it.each([
    ["no changes", [{ path: "a.txt", content: "one\n" }], "no changes"],
    ["bad path", [{ path: "../x", content: "x" }], "invalid path: ../x"],
    ["duplicate path", [{ path: "x", content: "1" }, { path: "x", content: "2" }], "duplicate path: x"],
    ["file under a file", [{ path: "a.txt/y", content: "1" }], "path conflicts with existing file or directory: a.txt/y"],
    ["file over a directory", [{ path: "dir", content: "1" }], "path conflicts with existing file or directory: dir"],
    ["delete a directory", [{ path: "dir", delete: true }], "invalid path: dir"],
  ])("rejects %s", async (_n, changes, message) => {
    const { store, tree } = await setup()
    await expect(applyChanges(store, tree, changes)).rejects.toThrow(message)
  })

  it("ignores deleting a missing file when something else changes", async () => {
    const { store, tree } = await setup()
    const next = await applyChanges(store, tree, [{ path: "nope", delete: true }, { path: "n", content: "n" }])
    expect(flattenTree(store, next).has("n")).toBe(true)
  })

  it("stores nothing when a change is invalid", async () => {
    const { store, tree } = await setup()
    const size = store.size
    await expect(applyChanges(store, tree, [{ path: "ok", content: "1" }, { path: "bad/..", content: "2" }])).rejects.toThrow()
    expect(store.size).toBe(size)
  })
})

describe("diffs", () => {
  it("diffs lines with context, line numbers and counts", () => {
    const d = diffText("a\nb\nc\n", "a\nB\nc\nd\n")
    expect(d.additions).toBe(2)
    expect(d.deletions).toBe(1)
    expect(d.hunks).toHaveLength(1)
    expect(d.hunks[0]).toMatchObject({ old_start: 1, old_lines: 3, new_start: 1, new_lines: 4 })
    expect(d.hunks[0].lines.map((l) => l.type)).toEqual(["context", "del", "add", "context", "add"])
  })

  it("has no hunks for equal texts and a zero start for an empty side", () => {
    expect(diffText("x\n", "x\n").hunks).toEqual([])
    expect(diffText("", "x\n").hunks[0]).toMatchObject({ old_start: 0, old_lines: 0, new_start: 1, new_lines: 1 })
  })

  it("flags files over the line limit as truncated", () => {
    const big = "x\n".repeat(5001)
    expect(diffText(big, "").truncated).toBe(true)
  })

  it("reports added, modified and deleted files sorted by path", async () => {
    const store: ObjectStore = new Map()
    const t1 = await applyChanges(store, "", [{ path: "b", content: "1\n" }, { path: "c", content: "x\n" }])
    const t2 = await applyChanges(store, t1, [{ path: "a", content: "new\n" }, { path: "b", content: "2\n" }, { path: "c", delete: true }])
    const files = diffTrees(store, t1, t2)
    expect(files.map((f) => [f.path, f.status])).toEqual([["a", "added"], ["b", "modified"], ["c", "deleted"]])
    expect(diffTrees(store, "", t1).every((f) => f.status === "added")).toBe(true)
  })

  it("putObject stores once", async () => {
    const store: ObjectStore = new Map()
    const a = await putObject(store, "blob", encodeText("hi"))
    expect(await putObject(store, "blob", encodeText("hi"))).toBe(a)
    expect(store.size).toBe(1)
  })
})
