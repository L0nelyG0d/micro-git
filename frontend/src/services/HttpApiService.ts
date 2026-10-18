import type { User } from "@/auth/AuthContext"
import { readPath } from "@/lib/git"
import type { Blob, Branch, CommitDetail, CommitsPage, Compare, Tree } from "@/lib/git"
import type { Repo, RepoList } from "@/lib/repos"
import type { ApiService } from "./ApiService"
import { request } from "./http"

const e = encodeURIComponent
const repoPath = (owner: string, repo: string) => `/repos/${e(owner)}/${e(repo)}`

/** Adds the non-empty entries of `q` as a query string ("" when there are none). */
function qs(q: Record<string, string | number | undefined> = {}): string {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(q)) if (v !== undefined && v !== "") p.set(k, String(v))
  const s = p.toString()
  return s ? `?${s}` : ""
}

/** The one ApiService implementation: HTTP to the backend through the /api proxy. */
export class HttpApiService implements ApiService {
  get<T>(path: string, options: { signal?: AbortSignal } = {}) {
    return request<T>(path, options)
  }

  health() {
    return request<void>("/health")
  }
  healthHead() {
    return request<void>("/health", { method: "HEAD" })
  }

  register(username: string, password: string) {
    return request<User>("/auth/register", { method: "POST", body: { username, password } })
  }
  login(username: string, password: string) {
    return request<User>("/auth/login", { method: "POST", body: { username, password } })
  }
  logout() {
    return request<void>("/auth/logout", { method: "POST" })
  }
  me(options = {}) {
    return request<User>("/auth/me", options)
  }

  listRepos(query = {}, options = {}) {
    return request<RepoList>(`/repos${qs(query)}`, options)
  }
  createRepo(input: Parameters<ApiService["createRepo"]>[0]) {
    return request<Repo>("/repos", { method: "POST", body: input })
  }
  getRepo(owner: string, repo: string, options = {}) {
    return request<Repo>(repoPath(owner, repo), options)
  }
  updateRepo(owner: string, repo: string, patch: Parameters<ApiService["updateRepo"]>[2]) {
    return request<Repo>(repoPath(owner, repo), { method: "PATCH", body: patch })
  }
  deleteRepo(owner: string, repo: string) {
    return request<void>(repoPath(owner, repo), { method: "DELETE" })
  }

  listCommits(owner: string, repo: string, query = {}, options = {}) {
    return request<CommitsPage>(`${repoPath(owner, repo)}/commits${qs(query)}`, options)
  }
  createCommit(owner: string, repo: string, input: Parameters<ApiService["createCommit"]>[2]) {
    return request<{ oid: string }>(`${repoPath(owner, repo)}/commits`, { method: "POST", body: input })
  }
  getCommit(owner: string, repo: string, oid: string, options = {}) {
    return request<CommitDetail>(`${repoPath(owner, repo)}/commits/${e(oid)}`, options)
  }
  compare(owner: string, repo: string, base: string, head: string, options = {}) {
    return request<Compare>(`${repoPath(owner, repo)}/compare${qs({ base, head })}`, options)
  }

  listBranches(owner: string, repo: string, options = {}) {
    return request<{ branches: Branch[] }>(`${repoPath(owner, repo)}/branches`, options)
  }
  createBranch(owner: string, repo: string, input: { name: string; from: string }) {
    return request<Branch>(`${repoPath(owner, repo)}/branches`, { method: "POST", body: input })
  }
  deleteBranch(owner: string, repo: string, branch: string) {
    return request<void>(`${repoPath(owner, repo)}/branches/${e(branch)}`, { method: "DELETE" })
  }
  mergeBranches(owner: string, repo: string, input: { base: string; head: string }) {
    return request<unknown>(`${repoPath(owner, repo)}/merge`, { method: "POST", body: input })
  }

  getTreeRoot(owner: string, repo: string, ref: string, options = {}) {
    return request<Tree>(readPath("tree", owner, repo, ref, ""), options)
  }
  getTree(owner: string, repo: string, ref: string, path: string, options = {}) {
    return request<Tree>(readPath("tree", owner, repo, ref, path), options)
  }
  getBlobRoot(owner: string, repo: string, ref: string, options = {}) {
    return request<Blob>(readPath("blob", owner, repo, ref, ""), options)
  }
  getBlob(owner: string, repo: string, ref: string, path: string, options = {}) {
    return request<Blob>(readPath("blob", owner, repo, ref, path), options)
  }
}
