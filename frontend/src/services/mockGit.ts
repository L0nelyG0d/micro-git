// Git objects, tree building and line diffs for MockApiService. A TypeScript
// port of backend/gitobj, backend/treebuild and backend/linediff: oids are the
// real git SHA-1 of "<type> <size>\0<body>", computed with Web Crypto.
import type { FileDiff, Hunk } from "@/lib/git"

export type ObjType = "blob" | "tree" | "commit"
export type GitObject = { type: ObjType; body: Uint8Array }
/** One repository's objects, keyed by oid. */
export type ObjectStore = Map<string, GitObject>

const enc = new TextEncoder()
const dec = new TextDecoder()
export const encodeText = (s: string) => enc.encode(s)
export const decodeText = (b: Uint8Array) => dec.decode(b)

const hex = (b: Uint8Array) => Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("")
const unhex = (s: string) => Uint8Array.from(s.match(/../g) ?? [], (h) => parseInt(h, 16))

export function isValidOid(s: string): boolean {
  return /^[0-9a-f]{40}$/.test(s)
}

function concat(...parts: Uint8Array[]): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0))
  let at = 0
  for (const p of parts) {
    out.set(p, at)
    at += p.length
  }
  return out
}

/** sha1("<type> <size>\0" + body) as 40 lowercase hex characters. */
export async function hashObject(type: ObjType, body: Uint8Array): Promise<string> {
  const data = concat(enc.encode(`${type} ${body.length}\0`), body)
  return hex(new Uint8Array(await crypto.subtle.digest("SHA-1", data)))
}

/** Hashes `body`, stores it unless already present, and returns its oid. */
export async function putObject(store: ObjectStore, type: ObjType, body: Uint8Array): Promise<string> {
  const oid = await hashObject(type, body)
  if (!store.has(oid)) store.set(oid, { type, body })
  return oid
}

// ---- trees ----

export type TreeEntry = { mode: "100644" | "40000"; name: string; oid: string }

/** Compares strings by their UTF-8 bytes, as the backend does (JS `<` compares UTF-16 code units). */
export function compareBytes(a: string, b: string): number {
  const x = enc.encode(a)
  const y = enc.encode(b)
  for (let i = 0; i < x.length && i < y.length; i++) if (x[i] !== y[i]) return x[i] - y[i]
  return x.length - y.length
}

/** Git tree order: bytewise (UTF-8) by name, a directory compares as name + "/". */
function sortEntries(entries: TreeEntry[]): TreeEntry[] {
  const key = (e: TreeEntry) => (e.mode === "40000" ? e.name + "/" : e.name)
  return [...entries].sort((a, b) => compareBytes(key(a), key(b)))
}

export function serializeTree(entries: TreeEntry[]): Uint8Array {
  return concat(
    ...sortEntries(entries).map((e) => concat(enc.encode(`${e.mode} ${e.name}\0`), unhex(e.oid))),
  )
}

export function parseTree(body: Uint8Array): TreeEntry[] {
  const out: TreeEntry[] = []
  let i = 0
  while (i < body.length) {
    const sp = body.indexOf(32, i)
    const nul = body.indexOf(0, sp)
    if (sp < 0 || nul < 0 || nul + 21 > body.length) throw new Error("malformed tree")
    const mode = dec.decode(body.subarray(i, sp)) as TreeEntry["mode"]
    out.push({ mode, name: dec.decode(body.subarray(sp + 1, nul)), oid: hex(body.subarray(nul + 1, nul + 21)) })
    i = nul + 21
  }
  return out
}

// ---- commits ----

export type Signature = { name: string; email: string; unix: number }
export type CommitData = { tree: string; parents: string[]; author: Signature; committer: Signature; message: string }

const sigText = (s: Signature) => `${s.name} <${s.email}> ${s.unix} +0000`

export function serializeCommit(c: CommitData): Uint8Array {
  const lines = [`tree ${c.tree}`, ...c.parents.map((p) => `parent ${p}`)]
  lines.push(`author ${sigText(c.author)}`, `committer ${sigText(c.committer)}`, "", c.message.trim() + "\n")
  return enc.encode(lines.join("\n"))
}

function parseSig(s: string): Signature {
  const m = /^(.*) <(.*)> (\d+) [+-]\d{4}$/.exec(s)
  if (!m) throw new Error("malformed signature")
  return { name: m[1], email: m[2], unix: Number(m[3]) }
}

export function parseCommit(body: Uint8Array): CommitData {
  const text = dec.decode(body)
  const cut = text.indexOf("\n\n")
  if (cut < 0) throw new Error("malformed commit")
  const c: Partial<CommitData> & { parents: string[] } = { parents: [] }
  for (const line of text.slice(0, cut).split("\n")) {
    const sp = line.indexOf(" ")
    const key = line.slice(0, sp)
    const val = line.slice(sp + 1)
    if (key === "tree") c.tree = val
    else if (key === "parent") c.parents.push(val)
    else if (key === "author") c.author = parseSig(val)
    else if (key === "committer") c.committer = parseSig(val)
  }
  if (!c.tree || !c.author || !c.committer) throw new Error("malformed commit")
  return { tree: c.tree, parents: c.parents, author: c.author, committer: c.committer, message: text.slice(cut + 2) }
}

/** Reads a commit object; undefined when the oid is missing or not a commit. */
export function getCommit(store: ObjectStore, oid: string): CommitData | undefined {
  const o = store.get(oid)
  return o?.type === "commit" ? parseCommit(o.body) : undefined
}

/** True when `ancestor` is `from` or reachable from it through parent links. */
export function reaches(store: ObjectStore, from: string, ancestor: string): boolean {
  const seen = new Set<string>()
  const todo = [from]
  while (todo.length) {
    const oid = todo.pop() as string
    if (oid === ancestor) return true
    if (seen.has(oid)) continue
    seen.add(oid)
    const c = getCommit(store, oid)
    if (c) todo.push(...c.parents)
  }
  return false
}

// ---- file maps and tree building ----

/** Every file below a root tree as path -> blob oid ("" is the empty tree). */
export function flattenTree(store: ObjectStore, root: string, prefix = "", out = new Map<string, string>()) {
  if (!root) return out
  const t = store.get(root)
  if (t?.type !== "tree") throw new Error(`missing tree ${root}`)
  for (const e of parseTree(t.body)) {
    if (e.mode === "40000") flattenTree(store, e.oid, `${prefix}${e.name}/`, out)
    else out.set(prefix + e.name, e.oid)
  }
  return out
}

/** Writes the trees for a path -> blob oid map and returns the root tree oid. */
async function writeTrees(store: ObjectStore, files: Map<string, string>): Promise<string> {
  type Dir = { files: Map<string, string>; dirs: Map<string, Dir> }
  const root: Dir = { files: new Map(), dirs: new Map() }
  for (const [path, oid] of files) {
    const segs = path.split("/")
    let d = root
    for (const s of segs.slice(0, -1)) {
      let next = d.dirs.get(s)
      if (!next) d.dirs.set(s, (next = { files: new Map(), dirs: new Map() }))
      d = next
    }
    d.files.set(segs[segs.length - 1], oid)
  }
  const write = async (d: Dir): Promise<string> => {
    const entries: TreeEntry[] = [...d.files].map(([name, oid]) => ({ mode: "100644", name, oid }))
    for (const [name, sub] of d.dirs) entries.push({ mode: "40000", name, oid: await write(sub) })
    return putObject(store, "tree", serializeTree(entries))
  }
  return write(root)
}

/** A validation failure with the message the backend sends (HTTP 400). */
export class GitError extends Error {}

export type FileChange = { path: string; content?: string; delete?: boolean }

const MAX_PATH = 255
const MAX_CONTENT = 1 << 20

function checkPath(p: string): void {
  const bad =
    p === "" || [...p].length > MAX_PATH || p.includes("\0") ||
    p.split("/").some((s) => s === "" || s === "." || s === ".." || s === ".git")
  if (bad) throw new GitError(`invalid path: ${p}`)
}

/**
 * Applies `changes` to the tree `root` ("" = empty) and returns the new root
 * tree oid. Same rules as backend/treebuild: invalid or duplicate paths, a file
 * where a directory is (and the reverse), oversize content and "no changes" are
 * GitErrors; deleting a missing file is ignored. New objects are stored only
 * when every change is valid.
 */
export async function applyChanges(store: ObjectStore, root: string, changes: FileChange[]): Promise<string> {
  const seen = new Set<string>()
  for (const c of changes) {
    checkPath(c.path)
    if ((c.content === undefined) === !c.delete) throw new GitError(`change must have content or delete: ${c.path}`)
    if (c.content !== undefined) {
      if (c.content.length > MAX_CONTENT || enc.encode(c.content).length > MAX_CONTENT) {
        throw new GitError(`file too large: ${c.path}`)
      }
      if (c.content.includes("\0")) throw new GitError(`file is not valid UTF-8 text: ${c.path}`)
    }
    if (seen.has(c.path)) throw new GitError(`duplicate path: ${c.path}`)
    seen.add(c.path)
  }
  for (const a of seen) {
    for (const b of seen) {
      if (b.startsWith(a + "/")) throw new GitError(`path conflicts with existing file or directory: ${b}`)
    }
  }

  const files = flattenTree(store, root)
  const pending = new Map(files)
  let changed = false
  const blobs: [string, Uint8Array][] = []
  for (const c of changes) {
    const underIt = [...files.keys()].some((f) => f.startsWith(c.path + "/"))
    if (c.delete) {
      if (underIt) throw new GitError(`invalid path: ${c.path}`) // deleting a directory
      if (pending.delete(c.path)) changed = true
      continue
    }
    const parts = c.path.split("/")
    for (let i = 1; i < parts.length; i++) {
      if (files.has(parts.slice(0, i).join("/"))) throw new GitError(`path conflicts with existing file or directory: ${c.path}`)
    }
    if (underIt) throw new GitError(`path conflicts with existing file or directory: ${c.path}`)
    const body = enc.encode(c.content as string)
    const oid = await hashObject("blob", body)
    if (pending.get(c.path) === oid) continue
    blobs.push([oid, body])
    pending.set(c.path, oid)
    changed = true
  }
  if (!changed) throw new GitError("no changes")
  for (const [oid, body] of blobs) if (!store.has(oid)) store.set(oid, { type: "blob", body })
  return writeTrees(store, pending)
}

// ---- diffs ----

const CONTEXT_LINES = 3
const MAX_LINES = 5000

type Op = { type: "context" | "add" | "del"; text: string }

/** "a" and "a\n" both give ["a"]; "" gives no lines. */
export function splitLines(s: string): string[] {
  return s === "" ? [] : s.replace(/\n$/, "").split("\n")
}

function lcsScript(a: string[], b: string[]): Op[] {
  const n = a.length
  const m = b.length
  const w = m + 1
  const t = new Uint16Array((n + 1) * w)
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      t[i * w + j] =
        a[i] === b[j] ? t[(i + 1) * w + j + 1] + 1 : Math.max(t[(i + 1) * w + j], t[i * w + j + 1])
    }
  }
  const ops: Op[] = []
  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      ops.push({ type: "context", text: a[i++] })
      j++
    } else if (t[(i + 1) * w + j] >= t[i * w + j + 1]) ops.push({ type: "del", text: a[i++] })
    else ops.push({ type: "add", text: b[j++] })
  }
  while (i < n) ops.push({ type: "del", text: a[i++] })
  while (j < m) ops.push({ type: "add", text: b[j++] })
  return ops
}

function buildHunks(ops: Op[]): Hunk[] {
  const spans: { lo: number; hi: number }[] = []
  ops.forEach((o, i) => {
    if (o.type === "context") return
    const lo = Math.max(0, i - CONTEXT_LINES)
    const hi = Math.min(ops.length - 1, i + CONTEXT_LINES)
    const last = spans[spans.length - 1]
    if (last && lo <= last.hi + 1) last.hi = hi
    else spans.push({ lo, hi })
  })
  const oldNo = [0]
  const newNo = [0]
  for (const o of ops) {
    oldNo.push(oldNo[oldNo.length - 1] + (o.type === "add" ? 0 : 1))
    newNo.push(newNo[newNo.length - 1] + (o.type === "del" ? 0 : 1))
  }
  return spans.map(({ lo, hi }) => {
    const old_lines = oldNo[hi + 1] - oldNo[lo]
    const new_lines = newNo[hi + 1] - newNo[lo]
    return {
      old_start: old_lines === 0 ? 0 : oldNo[lo] + 1,
      old_lines,
      new_start: new_lines === 0 ? 0 : newNo[lo] + 1,
      new_lines,
      lines: ops.slice(lo, hi + 1).map((o) => ({ type: o.type, text: o.text })),
    }
  })
}

/** Line diff of two texts: hunks with 3 lines of context, counts, truncation. */
export function diffText(oldText: string, newText: string) {
  const a = splitLines(oldText)
  const b = splitLines(newText)
  if (a.length > MAX_LINES || b.length > MAX_LINES) {
    return { hunks: [] as Hunk[], additions: 0, deletions: 0, truncated: true }
  }
  let pre = 0
  while (pre < a.length && pre < b.length && a[pre] === b[pre]) pre++
  let suf = 0
  while (suf < a.length - pre && suf < b.length - pre && a[a.length - 1 - suf] === b[b.length - 1 - suf]) suf++
  const ops: Op[] = [
    ...a.slice(0, pre).map((text): Op => ({ type: "context", text })),
    ...lcsScript(a.slice(pre, a.length - suf), b.slice(pre, b.length - suf)),
    ...a.slice(a.length - suf).map((text): Op => ({ type: "context", text })),
  ]
  return {
    hunks: buildHunks(ops),
    additions: ops.filter((o) => o.type === "add").length,
    deletions: ops.filter((o) => o.type === "del").length,
    truncated: false,
  }
}

/** Per-file diff of two root trees ("" = empty), sorted by path. */
export function diffTrees(store: ObjectStore, oldTree: string, newTree: string): FileDiff[] {
  const before = flattenTree(store, oldTree)
  const after = flattenTree(store, newTree)
  const text = (oid: string | undefined) => (oid ? decodeText((store.get(oid) as GitObject).body) : "")
  const paths = [...new Set([...before.keys(), ...after.keys()])].sort(compareBytes)
  const out: FileDiff[] = []
  for (const path of paths) {
    const o = before.get(path)
    const n = after.get(path)
    if (o === n) continue
    const d = diffText(text(o), text(n))
    out.push({
      path,
      status: o === undefined ? "added" : n === undefined ? "deleted" : "modified",
      additions: d.additions,
      deletions: d.deletions,
      truncated: d.truncated,
      hunks: d.hunks,
    })
  }
  return out
}
