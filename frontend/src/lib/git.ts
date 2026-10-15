import type { User } from "@/auth/AuthContext"
import { isOwner } from "@/auth/AuthContext"

export type Branch = { name: string; oid: string; is_default: boolean }
export type TreeEntry = { name: string; type: "tree" | "blob"; oid: string }
export type Tree = { ref: string; commit: string; path: string; entries: TreeEntry[] }
export type Blob = {
  ref: string
  commit: string
  path: string
  name: string
  oid: string
  size: number
  content: string
}
export type CommitSummary = {
  oid: string
  message: string
  author: string
  time: string
  parents: string[]
}

/** Branch names can never look like an oid, so a 40-hex ref is a commit. */
export function isOidRef(ref: string): boolean {
  return /^[0-9a-f]{40}$/.test(ref)
}

/** Edit / Add file / Create branch are for the owner on a branch only. */
export function canWrite(user: User | null, owner: string | undefined, ref: string): boolean {
  return isOwner(user, owner) && !isOidRef(ref)
}

const enc = (s: string) => s.split("/").map(encodeURIComponent).join("/")

/** API path for reading `kind` of `path` at `ref`. */
export function readPath(
  kind: "tree" | "blob",
  owner: string,
  repo: string,
  ref: string,
  path: string,
): string {
  const base = `/repos/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/${kind}/${encodeURIComponent(ref)}`
  return path ? `${base}/${enc(path)}` : base
}

/** UI route to a directory (kind "tree") or file (kind "blob"). */
export function uiPath(
  kind: "tree" | "blob",
  owner: string,
  repo: string,
  ref: string,
  path: string,
): string {
  const base = `/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/${kind}/${encodeURIComponent(ref)}`
  return path ? `${base}/${enc(path)}` : base
}
