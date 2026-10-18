// micro-git-mock-service: an in-browser ApiService. Selected only by
// services/index.ts when VITE_USE_MOCK=true; the production build leaves it out.
import type { User } from "@/auth/AuthContext"
import type { Blob, Branch, CommitDetail, CommitsPage, Compare, CommitSummary, Tree } from "@/lib/git"
import { invalidateAfterWrite } from "@/lib/fetchCache"
import type { Repo, RepoList } from "@/lib/repos"
import type { ApiService, CommitInput, ReadOptions, RepoPatch } from "./ApiService"
import { ApiError } from "./http"
import {
  applyChanges,
  compareBytes,
  decodeText,
  diffTrees,
  getCommit,
  GitError,
  isValidOid,
  parseTree,
  putObject,
  reaches,
  serializeCommit,
  type CommitData,
  type FileChange,
  type ObjectStore,
} from "./mockGit"

// Limits copied from the backend (auth.go, repos.go, commits.go, history.go).
const USERNAME_RE = /^[a-z0-9-]{3,32}$/
const REPO_NAME_RE = /^[A-Za-z0-9._-]{1,100}$/
const BRANCH_RE = /^[A-Za-z0-9._-]+$/
const MAX_DESCRIPTION = 500
const MAX_MESSAGE = 500
const MAX_CHANGES = 50
const DEFAULT_PER_PAGE = 50
const MAX_PER_PAGE = 100
const MAX_HISTORY = 10000

type MockUser = { id: number; username: string; password: string }
type MockRepo = {
  id: number
  ownerId: number
  name: string
  description: string
  defaultBranch: string
  private: boolean
  createdAt: string
  objects: ObjectStore
  refs: Map<string, string>
}

const fail = (status: number, message: string): never => {
  throw new ApiError(status, message)
}

/** Mirrors validBranchName in backend/commits.go. */
function validBranchName(n: unknown): n is string {
  return typeof n === "string" && BRANCH_RE.test(n) && n !== "." && n !== ".." && n.length <= 100 && !isValidOid(n)
}

const validRepoName = (n: unknown): n is string => typeof n === "string" && REPO_NAME_RE.test(n) && n !== "." && n !== ".."

function checkSignal(options?: ReadOptions) {
  if (options?.signal?.aborted) throw new DOMException("The operation was aborted.", "AbortError")
}

/** Strict positive-integer query value, as parsePage and parsePerPage in backend/history.go. */
function intParam(q: URLSearchParams, name: string, fallback: number, max = Infinity): number {
  if (!q.has(name)) return fallback
  const v = q.get(name) as string
  const n = Number(v)
  if (!/^[0-9]+$/.test(v) || !Number.isSafeInteger(n) || n < 1 || n > max) fail(400, `invalid ${name}`)
  return n
}

const rfc3339 = (unix: number) => new Date(unix * 1000).toISOString().replace(".000Z", "Z")

/** The in-browser ApiService: users, repositories and git objects live in memory until reload. */
export class MockApiService implements ApiService {
  private users: MockUser[] = []
  private repos: MockRepo[] = []
  private session: number | null = null
  private nextUserId = 1
  private nextRepoId = 1

  // ---- helpers ----

  private currentUser(): MockUser | undefined {
    return this.users.find((u) => u.id === this.session)
  }

  private requireUser(): MockUser {
    return this.currentUser() ?? fail(401, "authentication required")
  }

  private userName(id: number): string {
    return (this.users.find((u) => u.id === id) as MockUser).username
  }

  private view(r: MockRepo): Repo {
    return {
      id: r.id,
      owner: this.userName(r.ownerId),
      name: r.name,
      description: r.description,
      default_branch: r.defaultBranch,
      private: r.private,
      empty: r.refs.size === 0,
      created_at: r.createdAt,
    }
  }

  /** Repo lookup, case-insensitive; a private repo is 404 for everyone but its owner. */
  private findRepo(owner: string, name: string): MockRepo {
    const o = owner.toLowerCase()
    const n = name.toLowerCase()
    const repo = this.repos.find((r) => r.name.toLowerCase() === n && this.userName(r.ownerId) === o)
    if (!repo || (repo.private && repo.ownerId !== this.session)) return fail(404, "repository not found")
    return repo
  }

  /** findRepo, then the owner check (404 before 403). */
  private ownedRepo(owner: string, name: string): MockRepo {
    const repo = this.findRepo(owner, name)
    if (repo.ownerId !== this.session) fail(403, "forbidden")
    return repo
  }

  private nameTaken(ownerId: number, name: string, exceptId = 0): boolean {
    return this.repos.some((r) => r.ownerId === ownerId && r.id !== exceptId && r.name.toLowerCase() === name.toLowerCase())
  }

  private repoPath(r: MockRepo) {
    return `/repos/${encodeURIComponent(this.userName(r.ownerId))}/${encodeURIComponent(r.name)}`
  }

  /** Branch name first, else a full commit oid of this repository. */
  private resolveRef(repo: MockRepo, ref: string): string {
    const tip = repo.refs.get(ref)
    if (tip) return tip
    if (isValidOid(ref) && getCommit(repo.objects, ref)) return ref
    return fail(404, "ref not found")
  }

  private commitSummary(repo: MockRepo, oid: string): CommitSummary {
    const c = getCommit(repo.objects, oid) as CommitData
    return {
      oid,
      message: c.message.replace(/\n$/, ""),
      author: c.author.name,
      time: rfc3339(c.author.unix),
      parents: c.parents,
    }
  }

  private rootTree(repo: MockRepo, commit: string): string {
    return (getCommit(repo.objects, commit) as CommitData).tree
  }

  // ---- reads by path ----

  async get<T>(path: string, options: ReadOptions = {}): Promise<T> {
    checkSignal(options)
    const url = new URL(path, "http://mock.invalid")
    const seg = url.pathname.split("/").filter((s) => s !== "").map(decodeURIComponent)
    const q = url.searchParams
    const [head, owner, repo, kind, a] = seg
    let out: unknown
    if (head === "health" && seg.length === 1) out = undefined
    else if (head === "auth" && seg[1] === "me" && seg.length === 2) out = await this.me()
    else if (head === "repos" && seg.length === 1) {
      out = await this.listRepos({
        ...(q.has("owner") && { owner: q.get("owner") as string }),
        ...(q.has("page") && { page: intParam(q, "page", 1) }),
        ...(q.has("per_page") && { per_page: intParam(q, "per_page", DEFAULT_PER_PAGE, MAX_PER_PAGE) }),
      })
    } else if (head === "repos" && seg.length === 3) out = await this.getRepo(owner, repo)
    else if (head === "repos" && kind === "commits" && seg.length === 4) {
      out = await this.listCommits(owner, repo, {
        ...(q.has("ref") && { ref: q.get("ref") as string }),
        ...(q.has("page") && { page: intParam(q, "page", 1) }),
        ...(q.has("per_page") && { per_page: intParam(q, "per_page", DEFAULT_PER_PAGE, MAX_PER_PAGE) }),
      })
    } else if (head === "repos" && kind === "commits" && seg.length === 5) out = await this.getCommit(owner, repo, a)
    else if (head === "repos" && kind === "compare" && seg.length === 4) {
      out = await this.compare(owner, repo, q.get("base") ?? "", q.get("head") ?? "")
    } else if (head === "repos" && kind === "branches" && seg.length === 4) out = await this.listBranches(owner, repo)
    else if (head === "repos" && (kind === "tree" || kind === "blob") && seg.length >= 5) {
      out = this.read(kind, owner, repo, a, seg.slice(5).join("/") + (url.pathname.endsWith("/") ? "/" : ""))
    } else fail(404, "not found")
    checkSignal(options)
    return out as T
  }

  async health() {}
  async healthHead() {}

  // ---- auth ----

  async register(username: string, password: string): Promise<User> {
    const name = String(username ?? "").toLowerCase()
    if (!USERNAME_RE.test(name)) fail(400, "username must be 3-32 characters: a-z, 0-9 and -")
    if (name === "api") fail(400, "username is reserved")
    const bytes = new TextEncoder().encode(String(password ?? "")).length
    if (bytes < 8 || bytes > 72) fail(400, "password must be 8-72 bytes")
    if (this.users.some((u) => u.username === name)) fail(409, "username already taken")
    const user = { id: this.nextUserId++, username: name, password }
    this.users.push(user)
    // Like the backend, register sets no session: the caller logs in next.
    invalidateAfterWrite("/auth/register")
    return { id: user.id, username: user.username }
  }

  async login(username: string, password: string): Promise<User> {
    if (!username || !password) fail(400, "username and password are required")
    const user = this.users.find((u) => u.username === username.toLowerCase())
    if (!user || user.password !== password) fail(401, "invalid username or password")
    this.session = (user as MockUser).id
    invalidateAfterWrite("/auth/login")
    return { id: (user as MockUser).id, username: (user as MockUser).username }
  }

  async logout(): Promise<void> {
    this.session = null
    invalidateAfterWrite("/auth/logout")
  }

  async me(options: ReadOptions = {}): Promise<User> {
    checkSignal(options)
    const u = this.requireUser()
    return { id: u.id, username: u.username }
  }

  // ---- repositories ----

  async listRepos(query: { owner?: string; page?: number; per_page?: number } = {}, options: ReadOptions = {}): Promise<RepoList> {
    checkSignal(options)
    const page = query.page ?? 1
    const perPage = query.per_page ?? DEFAULT_PER_PAGE
    if (!Number.isInteger(page) || page < 1) fail(400, "invalid page")
    if (!Number.isInteger(perPage) || perPage < 1 || perPage > MAX_PER_PAGE) fail(400, "invalid per_page")
    const owner = query.owner?.toLowerCase()
    const visible = this.repos
      .filter((r) => !r.private || r.ownerId === this.session)
      .filter((r) => owner === undefined || this.userName(r.ownerId) === owner)
      .sort((x, y) => (x.createdAt === y.createdAt ? y.id - x.id : x.createdAt < y.createdAt ? 1 : -1))
    const from = (page - 1) * perPage
    return {
      repos: visible.slice(from, from + perPage).map((r) => this.view(r)),
      page,
      per_page: perPage,
      has_more: visible.length > from + perPage,
    }
  }

  async createRepo(input: { name: string; description?: string; private?: boolean }): Promise<Repo> {
    const user = this.requireUser()
    if (!validRepoName(input?.name)) fail(400, "invalid repository name")
    const description = input.description ?? ""
    if ([...description].length > MAX_DESCRIPTION) fail(400, "description must be at most 500 characters")
    if (this.nameTaken(user.id, input.name)) fail(409, "repository already exists")
    const repo: MockRepo = {
      id: this.nextRepoId++,
      ownerId: user.id,
      name: input.name,
      description,
      defaultBranch: "main",
      private: input.private === true,
      createdAt: new Date().toISOString(),
      objects: new Map(),
      refs: new Map(),
    }
    this.repos.push(repo)
    invalidateAfterWrite("/repos")
    return this.view(repo)
  }

  async getRepo(owner: string, repo: string, options: ReadOptions = {}): Promise<Repo> {
    checkSignal(options)
    return this.view(this.findRepo(owner, repo))
  }

  async updateRepo(owner: string, repo: string, patch: RepoPatch): Promise<Repo> {
    this.requireUser()
    const r = this.ownedRepo(owner, repo)
    const oldPath = this.repoPath(r)
    if (!patch || (patch.name === undefined && patch.description === undefined && patch.default_branch === undefined && patch.private === undefined)) {
      fail(400, "invalid JSON body")
    }
    if (patch.name !== undefined && !validRepoName(patch.name)) fail(400, "invalid repository name")
    if (patch.description !== undefined && [...patch.description].length > MAX_DESCRIPTION) {
      fail(400, "description must be at most 500 characters")
    }
    if (patch.default_branch !== undefined && !r.refs.has(patch.default_branch)) fail(404, "branch not found")
    if (patch.name !== undefined && this.nameTaken(r.ownerId, patch.name, r.id)) fail(409, "repository already exists")
    if (patch.name !== undefined) r.name = patch.name
    if (patch.description !== undefined) r.description = patch.description
    if (patch.default_branch !== undefined) r.defaultBranch = patch.default_branch
    if (patch.private !== undefined) r.private = patch.private
    invalidateAfterWrite(oldPath)
    invalidateAfterWrite(this.repoPath(r))
    return this.view(r)
  }

  async deleteRepo(owner: string, repo: string): Promise<void> {
    this.requireUser()
    const r = this.ownedRepo(owner, repo)
    this.repos = this.repos.filter((x) => x !== r)
    invalidateAfterWrite(this.repoPath(r))
  }

  // ---- commits ----

  async listCommits(
    owner: string,
    repo: string,
    query: { ref?: string; page?: number; per_page?: number } = {},
    options: ReadOptions = {},
  ): Promise<CommitsPage> {
    checkSignal(options)
    const r = this.findRepo(owner, repo)
    const page = query.page ?? 1
    const perPage = query.per_page ?? DEFAULT_PER_PAGE
    if (!Number.isInteger(page) || page < 1) fail(400, "invalid page")
    if (!Number.isInteger(perPage) || perPage < 1 || perPage > MAX_PER_PAGE) fail(400, "invalid per_page")
    if (page > MAX_HISTORY || page * perPage > MAX_HISTORY) fail(400, "invalid page")
    const ref = query.ref ?? r.defaultBranch
    let oid: string | undefined = this.resolveRef(r, ref)
    const skip = (page - 1) * perPage
    const commits: CommitSummary[] = []
    let seen = 0
    let hasMore = false
    // First-parent walk from the tip.
    while (oid) {
      if (seen >= skip + perPage) {
        hasMore = true
        break
      }
      if (seen >= skip) commits.push(this.commitSummary(r, oid))
      seen++
      oid = (getCommit(r.objects, oid) as CommitData).parents[0]
    }
    return { ref, page, per_page: perPage, has_more: hasMore, commits }
  }

  async createCommit(owner: string, repo: string, input: CommitInput): Promise<{ oid: string }> {
    const user = this.requireUser()
    const r = this.findRepo(owner, repo)
    const branch = input?.branch
    const isEmpty = r.refs.size === 0
    // A missing branch is a 404 that comes before the 403.
    if (validBranchName(branch) && !isEmpty && !r.refs.has(branch)) fail(404, "branch not found")
    if (r.ownerId !== user.id) fail(403, "forbidden")

    if (!validBranchName(branch)) fail(400, "invalid branch name")
    const expected = input.expected_tip ?? ""
    if (expected !== "" && !isValidOid(expected)) fail(400, "invalid expected_tip")
    const message = String(input.message ?? "").trim()
    if ([...message].length < 1 || [...message].length > MAX_MESSAGE) fail(400, "message must be 1-500 characters")
    const changes = input.changes
    if (!Array.isArray(changes) || changes.length < 1 || changes.length > MAX_CHANGES) {
      fail(400, "changes must contain 1-50 entries")
    }
    if (changes.some((c) => typeof c !== "object" || c === null)) fail(400, "invalid JSON body")
    if (isEmpty && branch !== r.defaultBranch) fail(400, "first commit must target the default branch")

    const tip = r.refs.get(branch) ?? ""
    const root = tip ? this.rootTree(r, tip) : ""
    const fileChanges: FileChange[] = changes.map((c) => ("delete" in c ? { path: c.path, delete: true } : { path: c.path, content: c.content }))
    let tree: string
    try {
      tree = await applyChanges(r.objects, root, fileChanges)
    } catch (e) {
      if (e instanceof GitError) return fail(400, e.message)
      throw e
    }
    if (expected !== tip) fail(409, "branch tip has moved")

    const sig = { name: user.username, email: `${user.username}@micro-git.local`, unix: Math.floor(Date.now() / 1000) }
    const oid = await putObject(
      r.objects,
      "commit",
      serializeCommit({ tree, parents: tip ? [tip] : [], author: sig, committer: sig, message }),
    )
    r.refs.set(branch, oid)
    invalidateAfterWrite(this.repoPath(r))
    return { oid, tree, parent: tip || null, branch } as { oid: string }
  }

  async getCommit(owner: string, repo: string, oid: string, options: ReadOptions = {}): Promise<CommitDetail> {
    checkSignal(options)
    const r = this.findRepo(owner, repo)
    if (!isValidOid(oid)) fail(400, "invalid commit id")
    const c = getCommit(r.objects, oid) ?? fail(404, "commit not found")
    const parent = c.parents[0] ? this.rootTree(r, c.parents[0]) : ""
    return { ...this.commitSummary(r, oid), tree: c.tree, files: diffTrees(r.objects, parent, c.tree) }
  }

  async compare(owner: string, repo: string, base: string, head: string, options: ReadOptions = {}): Promise<Compare> {
    checkSignal(options)
    const r = this.findRepo(owner, repo)
    if (!base || !head) fail(400, "base and head are required")
    const baseCommit = this.resolveRef(r, base)
    const headCommit = this.resolveRef(r, head)
    return {
      base,
      head,
      base_commit: baseCommit,
      head_commit: headCommit,
      files: diffTrees(r.objects, this.rootTree(r, baseCommit), this.rootTree(r, headCommit)),
    }
  }

  // ---- branches ----

  async listBranches(owner: string, repo: string, options: ReadOptions = {}): Promise<{ branches: Branch[] }> {
    checkSignal(options)
    const r = this.findRepo(owner, repo)
    const branches = [...r.refs]
      .sort(([x], [y]) => compareBytes(x, y))
      .map(([name, oid]) => ({ name, oid, is_default: name === r.defaultBranch }))
    return { branches }
  }

  async createBranch(owner: string, repo: string, input: { name: string; from: string }): Promise<Branch> {
    this.requireUser()
    const r = this.findRepo(owner, repo)
    const tip = r.refs.get(input?.from) ?? fail(404, "branch not found")
    if (r.ownerId !== this.session) fail(403, "forbidden")
    if (!validBranchName(input.name)) fail(400, "invalid branch name")
    if (r.refs.has(input.name)) fail(409, "branch already exists")
    r.refs.set(input.name, tip)
    invalidateAfterWrite(this.repoPath(r))
    return { name: input.name, oid: tip, is_default: input.name === r.defaultBranch }
  }

  async deleteBranch(owner: string, repo: string, branch: string): Promise<void> {
    this.requireUser()
    const r = this.findRepo(owner, repo)
    if (!r.refs.has(branch)) fail(404, "branch not found")
    if (r.ownerId !== this.session) fail(403, "forbidden")
    if (branch === r.defaultBranch) fail(400, "cannot delete the default branch")
    r.refs.delete(branch)
    invalidateAfterWrite(this.repoPath(r))
  }

  /** Fast-forward only, as in backend/merge.go. */
  async mergeBranches(owner: string, repo: string, input: { base: string; head: string }): Promise<unknown> {
    this.requireUser()
    const r = this.findRepo(owner, repo)
    const { base, head } = input ?? {}
    let baseTip = ""
    let headTip = ""
    if (validBranchName(base) && validBranchName(head)) {
      baseTip = r.refs.get(base) ?? fail(404, "branch not found")
      headTip = r.refs.get(head) ?? fail(404, "branch not found")
    }
    if (r.ownerId !== this.session) fail(403, "forbidden")
    if (!validBranchName(base) || !validBranchName(head)) fail(400, "invalid branch name")
    if (base === head) fail(400, "cannot merge a branch into itself")
    if (reaches(r.objects, baseTip, headTip)) fail(400, "already up to date")
    if (!reaches(r.objects, headTip, baseTip)) fail(409, "not a fast-forward")
    r.refs.set(base, headTip)
    invalidateAfterWrite(this.repoPath(r))
    return { branch: base, oid: headTip, previous_oid: baseTip }
  }

  // ---- tree and blob ----

  /** Shared front half of the tree and blob reads: repo, ref, path validation, lookup. */
  private target(owner: string, repo: string, ref: string, rawPath: string) {
    const r = this.findRepo(owner, repo)
    const commit = this.resolveRef(r, ref)
    const path = rawPath.replace(/\/$/, "")
    if (path !== "" && path.split("/").some((s) => s === "" || s === "." || s === ".." || s === ".git" || s.includes("\0"))) {
      fail(400, "invalid path")
    }
    let oid = this.rootTree(r, commit)
    let isDir = true
    for (const name of path === "" ? [] : path.split("/")) {
      if (!isDir) fail(404, "path not found")
      const entry = parseTree(r.objects.get(oid)!.body).find((e) => e.name === name) ?? fail(404, "path not found")
      oid = entry.oid
      isDir = entry.mode === "40000"
    }
    return { r, commit, path, oid, isDir }
  }

  private read(kind: "tree" | "blob", owner: string, repo: string, ref: string, rawPath: string): Tree | Blob {
    const { r, commit, path, oid, isDir } = this.target(owner, repo, ref, rawPath)
    if (kind === "tree") {
      if (!isDir) fail(400, "not a directory")
      const entries = parseTree(r.objects.get(oid)!.body).map((e) => ({
        name: e.name,
        type: e.mode === "40000" ? ("tree" as const) : ("blob" as const),
        oid: e.oid,
      }))
      // Directories first, then files, each bytewise by name.
      entries.sort((x, y) => (x.type !== y.type ? (x.type === "tree" ? -1 : 1) : compareBytes(x.name, y.name)))
      return { ref, commit, path, entries }
    }
    if (isDir || path === "") fail(400, "not a file")
    const body = r.objects.get(oid)!.body
    return { ref, commit, path, name: path.slice(path.lastIndexOf("/") + 1), oid, size: body.length, content: decodeText(body) }
  }

  async getTreeRoot(owner: string, repo: string, ref: string, options: ReadOptions = {}): Promise<Tree> {
    checkSignal(options)
    return this.read("tree", owner, repo, ref, "") as Tree
  }
  async getTree(owner: string, repo: string, ref: string, path: string, options: ReadOptions = {}): Promise<Tree> {
    checkSignal(options)
    return this.read("tree", owner, repo, ref, path) as Tree
  }
  async getBlobRoot(owner: string, repo: string, ref: string, options: ReadOptions = {}): Promise<Blob> {
    checkSignal(options)
    return this.read("blob", owner, repo, ref, "") as Blob
  }
  async getBlob(owner: string, repo: string, ref: string, path: string, options: ReadOptions = {}): Promise<Blob> {
    checkSignal(options)
    return this.read("blob", owner, repo, ref, path) as Blob
  }
}
