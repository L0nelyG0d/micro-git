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

/** Mirrors the server's validBranchName (backend/commits.go): charset, not . or .., at most 100, not an oid. */
export function validateBranchName(name: string): string {
  const ok = /^[A-Za-z0-9._-]+$/.test(name) && name !== "." && name !== ".." && name.length <= 100 && !isOidRef(name)
  return ok ? "" : "invalid branch name"
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

export type CommitsPage = {
  ref: string
  page: number
  per_page: number
  has_more: boolean
  commits: CommitSummary[]
}
export type DiffLine = { type: "context" | "add" | "del"; text: string }
export type Hunk = {
  old_start: number
  old_lines: number
  new_start: number
  new_lines: number
  lines: DiffLine[]
}
export type FileDiff = {
  path: string
  status: "added" | "modified" | "deleted"
  additions: number
  deletions: number
  truncated: boolean
  hunks: Hunk[]
}
export type CommitDetail = CommitSummary & { tree: string; files: FileDiff[] }

/** UI route to a commit (full oid). */
export function commitPath(owner: string, repo: string, oid: string): string {
  return `/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/commit/${oid}`
}

/** UI route to the history of `ref`, page 1 without a query. */
export function historyPath(owner: string, repo: string, ref: string, page = 1): string {
  const base = `/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/commits/${encodeURIComponent(ref)}`
  return page > 1 ? `${base}?page=${page}` : base
}

/** Response of `GET /repos/{owner}/{repo}/compare`. */
export type Compare = { base: string; head: string; base_commit: string; head_commit: string; files: FileDiff[] }

/** One file change in a commit request. */
export type Change = { path: string; content: string } | { path: string; delete: true }
