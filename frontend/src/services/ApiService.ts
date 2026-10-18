import type { User } from "@/auth/AuthContext"
import type { Blob, Branch, Change, CommitDetail, CommitsPage, Compare, Tree } from "@/lib/git"
import type { Repo, RepoList } from "@/lib/repos"

export type ReadOptions = { signal?: AbortSignal }

/** Body of `PATCH /repos/{owner}/{repo}`: any subset of the fields. */
export type RepoPatch = { name?: string; description?: string; default_branch?: string; private?: boolean }

/** Body of `POST /repos/{owner}/{repo}/commits`. */
export type CommitInput = { branch: string; expected_tip: string; message: string; changes: Change[] }

/**
 * Every network operation of the UI, one method per `operationId` in
 * openapi.yaml, plus `get` for path-addressed reads (used by useFetch).
 * Pages and hooks use the instance from `@/services`, never `fetch`.
 */
export interface ApiService {
  /** Path-addressed GET, the read primitive behind useFetch. */
  get<T>(path: string, options?: ReadOptions): Promise<T>

  health(): Promise<void>
  healthHead(): Promise<void>

  register(username: string, password: string): Promise<User>
  login(username: string, password: string): Promise<User>
  logout(): Promise<void>
  me(options?: ReadOptions): Promise<User>

  listRepos(query?: { owner?: string; page?: number; per_page?: number }, options?: ReadOptions): Promise<RepoList>
  createRepo(input: { name: string; description?: string; private?: boolean }): Promise<Repo>
  getRepo(owner: string, repo: string, options?: ReadOptions): Promise<Repo>
  updateRepo(owner: string, repo: string, patch: RepoPatch): Promise<Repo>
  deleteRepo(owner: string, repo: string): Promise<void>

  listCommits(
    owner: string,
    repo: string,
    query?: { ref?: string; page?: number; per_page?: number },
    options?: ReadOptions,
  ): Promise<CommitsPage>
  createCommit(owner: string, repo: string, input: CommitInput): Promise<{ oid: string }>
  getCommit(owner: string, repo: string, oid: string, options?: ReadOptions): Promise<CommitDetail>
  compare(owner: string, repo: string, base: string, head: string, options?: ReadOptions): Promise<Compare>

  listBranches(owner: string, repo: string, options?: ReadOptions): Promise<{ branches: Branch[] }>
  createBranch(owner: string, repo: string, input: { name: string; from: string }): Promise<Branch>
  deleteBranch(owner: string, repo: string, branch: string): Promise<void>
  mergeBranches(owner: string, repo: string, input: { base: string; head: string }): Promise<unknown>

  getTreeRoot(owner: string, repo: string, ref: string, options?: ReadOptions): Promise<Tree>
  getTree(owner: string, repo: string, ref: string, path: string, options?: ReadOptions): Promise<Tree>
  getBlobRoot(owner: string, repo: string, ref: string, options?: ReadOptions): Promise<Blob>
  getBlob(owner: string, repo: string, ref: string, path: string, options?: ReadOptions): Promise<Blob>
}
