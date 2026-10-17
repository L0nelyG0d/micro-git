import { Link, useNavigate, useParams } from "react-router-dom"
import { useAuth, isOwner } from "@/auth/AuthContext"
import { BranchSwitcher } from "@/components/BranchSwitcher"
import { Breadcrumbs } from "@/components/Breadcrumbs"
import { DeleteRepoDialog } from "@/components/DeleteRepoDialog"
import { FileTree } from "@/components/FileTree"
import { LatestCommitBar } from "@/components/LatestCommitBar"
import { MergeDialog } from "@/components/MergeDialog"
import { ReadmeCard } from "@/components/ReadmeCard"
import { RenameRepoDialog } from "@/components/RenameRepoDialog"
import { ErrorBlock,LoadingBlock, NotFoundBlock } from "@/components/StateViews"
import { PageToolbar } from "@/components/PageToolbar"
import { Section } from "@/components/Section"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { ApiError } from "@/lib/api"
import { formatDate } from "@/lib/format"
import { canWrite, readPath, uiPath, type Blob, type Branch, type CommitSummary, type Tree } from "@/lib/git"
import type { Repo } from "@/lib/repos"
import { useFetch } from "@/lib/useFetch"

/** `/:owner/:repo` (default branch) and `/:owner/:repo/tree/:ref/*`. */
export function RepoPage() {
  const { owner = "", repo: name = "", ref: refParam, "*": splat = "" } = useParams()
  const path = splat.replace(/\/+$/, "")
  const navigate = useNavigate()
  const { user, expireSession } = useAuth()

  const repoQ = useFetch<Repo>(`/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`)
  const repo = repoQ.data
  const ref = refParam ?? repo?.default_branch ?? ""
  const base = `/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`

  // Nothing below waits for the repo request unless it needs the default branch
  // (no ref in the URL); with a ref all five requests start together. A missing
  // or empty repo is decided from repoQ, and the extra answers are ignored.
  const branchesQ = useFetch<{ branches: Branch[] }>(`${base}/branches`)
  // The default branch is only used once the repo is known to have commits.
  const fetchRef = refParam ?? (repo && !repo.empty ? repo.default_branch : "")
  const treeQ = useFetch<Tree>(fetchRef ? readPath("tree", owner, name, fetchRef, path) : null)
  const commitsQ = useFetch<{ commits: CommitSummary[] }>(
    // Without a ref the API answers for the default branch.
    `${base}/commits?${refParam ? `ref=${encodeURIComponent(refParam)}&` : ""}page=1`,
  )
  const readmeQ = useFetch<Blob>(fetchRef && !path ? readPath("blob", owner, name, fetchRef, "README.md") : null)

  const retryAll = () => {
    repoQ.reload()
    branchesQ.reload()
    treeQ.reload()
    commitsQ.reload()
    readmeQ.reload()
  }

  if (repoQ.error instanceof ApiError && repoQ.error.status === 404) {
    return (
      <NotFoundBlock
        title="Repository not found"
        hint={`There is no repository ${owner}/${name}. Check the address or go back to the list.`}
      />
    )
  }
  if (repoQ.error) return <ErrorBlock error={repoQ.error} onRetry={retryAll} />
  if (!repo) return <LoadingBlock label="Loading repository" />

  const owns = isOwner(user, owner)
  const writable = canWrite(user, owner, ref)

  const header = (
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        <h1 className="text-xl font-semibold break-all">
          {repo.owner}/{repo.name}
        </h1>
        {repo.description && <p className="text-sm text-muted-foreground">{repo.description}</p>}
      </div>
      {owns && (
        <div className="flex flex-wrap gap-2">
          <RenameRepoDialog
            owner={owner}
            repo={name}
            onUnauthorized={expireSession}
            onRenamed={(n) => navigate(`/${encodeURIComponent(owner)}/${encodeURIComponent(n)}`, { replace: true })}
          />
          <DeleteRepoDialog
            owner={owner}
            repo={name}
            onUnauthorized={expireSession}
            onDeleted={() => navigate("/")}
          />
        </div>
      )}
    </div>
  )

  if (repo.empty) {
    return (
      <div className="flex flex-col gap-3">
        <PageToolbar up={{ to: "/", label: "all repositories" }} />
        {header}
        <div className="flex flex-col items-start gap-2 rounded-xl bg-card p-3 text-sm shadow-xs ring-1 ring-border">
          {owns ? (
            <>
              <p className="text-muted-foreground">This repository is empty. Add the first file to create the {repo.default_branch} branch.</p>
              <Button asChild size="sm">
                <Link to={`/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/new/${encodeURIComponent(repo.default_branch)}`}>
                  Add file
                </Link>
              </Button>
            </>
          ) : (
            <p className="text-muted-foreground">This repository is empty. Nothing has been committed yet.</p>
          )}
        </div>
      </div>
    )
  }

  const treeNotFound =
    treeQ.error instanceof ApiError && (treeQ.error.status === 404 || treeQ.error.status === 400)
  const firstError = branchesQ.error ?? (treeNotFound ? undefined : treeQ.error)
  const commit = commitsQ.data?.commits[0]

  let body
  if (firstError) {
    body = <ErrorBlock error={firstError} onRetry={retryAll} />
  } else if (treeNotFound || (treeQ.data && path && treeQ.data.entries.length === 0)) {
    body = (
      <div className="flex flex-col gap-1" data-testid="not-found">
        <h2 className="font-semibold">Path not found</h2>
        <p className="text-sm text-muted-foreground">
          {path ? `${path} does not exist` : "This branch does not exist"} at {ref}. Go back to the{" "}
          <Link to={uiPath("tree", owner, name, repo.default_branch, "")} className="text-primary underline underline-offset-2">
            default branch
          </Link>
          .
        </p>
      </div>
    )
  } else if (!treeQ.data || !branchesQ.data) {
    body = <LoadingBlock label="Loading files" />
  } else {
    body = (
      <>
        {commit && <LatestCommitBar owner={owner} repo={name} commit={commit} historyRef={ref} />}
        <Section title="Files" className="[&>div]:p-0">
          <FileTree owner={owner} repo={name} ref_={ref} path={path} entries={treeQ.data.entries} />
        </Section>
        {!path && treeQ.data.entries.some((e) => e.type === "blob" && e.name === "README.md") && (
          <ReadmeCard q={readmeQ} />
        )}
      </>
    )
  }

  const repoBase = `/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`
  const parentDir = path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : ""
  const up = path
    ? { to: uiPath("tree", owner, name, ref, parentDir), label: parentDir || name }
    : { to: "/", label: "all repositories" }

  return (
    <div className="flex flex-col gap-3">
      <PageToolbar up={up}>
        <Breadcrumbs owner={owner} repo={name} ref_={ref} path={path} />
      </PageToolbar>
      {header}
      <div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_14rem]">
        <div className="flex min-w-0 flex-col gap-3">{body}</div>
        <aside aria-label="Repository details" className="order-first flex min-w-0 flex-col gap-3 md:order-last">
          <Section title="About" className="hidden md:flex">
            <p className="text-sm break-words text-muted-foreground">
              {repo.description || "No description."}
            </p>
            <p className="text-xs text-muted-foreground">Created {formatDate(repo.created_at)}</p>
          </Section>
          <Section title="Branch">
            {branchesQ.data ? (
              <BranchSwitcher
                owner={owner}
                repo={name}
                current={ref}
                branches={branchesQ.data.branches}
                canCreate={writable}
                onSelect={(b) => navigate(uiPath("tree", owner, name, b, ""))}
                onCreated={(b) => {
                  branchesQ.reload()
                  navigate(uiPath("tree", owner, name, b, ""))
                }}
                onUnauthorized={expireSession}
                canDelete={owns}
                onDeleted={(b) => {
                  branchesQ.reload()
                  if (b === ref) navigate(uiPath("tree", owner, name, repo.default_branch, ""))
                }}
              />
            ) : (
              <Skeleton className="h-8 w-full" />
            )}
          </Section>
          <Section title="Actions">
            <div className="flex flex-col items-stretch gap-2 [&>*]:w-full">
              {writable && (
                <Button asChild size="sm">
                  <Link to={`${repoBase}/new/${encodeURIComponent(ref)}`}>Add file</Link>
                </Button>
              )}
              <Button asChild size="sm" variant="outline">
                <Link to={`${repoBase}/compare`}>Compare</Link>
              </Button>
              {owns && (
                <Button asChild size="sm" variant="outline">
                  <Link to={`${repoBase}/settings`}>Settings</Link>
                </Button>
              )}
              {owns && branchesQ.data && branchesQ.data.branches.length >= 2 && (
                <MergeDialog
                  owner={owner}
                  repo={name}
                  branches={branchesQ.data.branches}
                  defaultBranch={repo.default_branch}
                  current={ref}
                  onUnauthorized={expireSession}
                  onReload={retryAll}
                  onMerged={(into) => {
                    branchesQ.reload()
                    if (into !== ref) navigate(uiPath("tree", owner, name, into, ""))
                    else {
                      treeQ.reload()
                      commitsQ.reload()
                      readmeQ.reload()
                    }
                  }}
                />
              )}
            </div>
          </Section>
        </aside>
      </div>
    </div>
  )
}
