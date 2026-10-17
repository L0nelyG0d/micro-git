import { Link, useParams, useSearchParams } from "react-router-dom"
import { FileView } from "@/components/DiffView"
import { PageToolbar } from "@/components/PageToolbar"
import { ErrorBlock, LoadingBlock, NotFoundBlock } from "@/components/StateViews"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { ApiError } from "@/lib/api"
import { shortOid } from "@/lib/format"
import { isOidRef, uiPath, type Branch, type FileDiff } from "@/lib/git"
import type { Repo } from "@/lib/repos"
import { useFetch } from "@/lib/useFetch"

type Compare = { base: string; head: string; base_commit: string; head_commit: string; files: FileDiff[] }

function RefSelect({ label, value, branches, onChange }: {
  label: string
  value: string
  branches: Branch[]
  onChange: (v: string) => void
}) {
  const known = branches.some((b) => b.name === value)
  return (
    <div className="flex items-center gap-2 text-sm">
      <span aria-hidden>{label}</span>
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger aria-label={label} className="w-48">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {!known && <SelectItem value={value}>{isOidRef(value) ? shortOid(value) : value}</SelectItem>}
          {branches.map((b) => (
            <SelectItem key={b.name} value={b.name}>
              {b.name}
              {b.is_default ? " (default)" : ""}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}

/**
 * `/:owner/:repo/compare?base=&head=`. Query values win; a missing one falls
 * back to the default branch (simplest option), which shows "No differences".
 */
export function ComparePage() {
  const { owner = "", repo: name = "" } = useParams()
  const [params, setParams] = useSearchParams()
  const apiBase = `/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`

  const repoQ = useFetch<Repo>(apiBase)
  const repo = repoQ.data
  const live = repo && !repo.empty ? repo : undefined
  const branchesQ = useFetch<{ branches: Branch[] }>(`${apiBase}/branches`) // parallel with the repo request

  const base = params.get("base") || repo?.default_branch || ""
  const head = params.get("head") || repo?.default_branch || ""
  // Both branches in the query: no need to wait for the default branch.
  const needsRepo = !params.get("base") || !params.get("head")
  const compareQ = useFetch<Compare>(
    (needsRepo ? live : true) && base && head ? `${apiBase}/compare?base=${encodeURIComponent(base)}&head=${encodeURIComponent(head)}` : null,
  )

  const retryAll = () => {
    repoQ.reload()
    branchesQ.reload()
    compareQ.reload()
  }
  const set = (key: "base" | "head", value: string) => {
    const next = new URLSearchParams(params)
    next.set("base", base)
    next.set("head", head)
    next.set(key, value)
    setParams(next)
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

  const back = (
    <PageToolbar up={{ to: uiPath("tree", owner, name, repo.default_branch, ""), label: "files" }}>
      <Link to={uiPath("tree", owner, name, repo.default_branch, "")} className="text-sm text-primary hover:underline">
        {owner}/{name}
      </Link>
    </PageToolbar>
  )
  if (repo.empty) {
    return (
      <div className="flex flex-col gap-3">
        {back}
        <p className="rounded-xl bg-card p-3 text-sm text-muted-foreground shadow-xs ring-1 ring-border">
          This repository is empty. There is nothing to compare until a file is committed.
        </p>
      </div>
    )
  }

  const refNotFound = compareQ.error instanceof ApiError && compareQ.error.status === 404
  const error = branchesQ.error ?? (refNotFound ? undefined : compareQ.error)
  const files = compareQ.data?.files
  const adds = files?.reduce((s, f) => s + f.additions, 0) ?? 0
  const dels = files?.reduce((s, f) => s + f.deletions, 0) ?? 0

  let body
  if (error) {
    body = <ErrorBlock error={error} onRetry={retryAll} />
  } else if (refNotFound) {
    body = (
      <p role="alert" className="text-sm text-destructive">
        One of the selected branches no longer exists. Pick another one.
      </p>
    )
  } else if (!files) {
    body = <LoadingBlock label="Loading comparison" />
  } else if (files.length === 0) {
    body = (
      <p className="rounded-xl bg-card p-3 text-sm text-muted-foreground shadow-xs ring-1 ring-border">
        No differences between these branches
      </p>
    )
  } else {
    body = (
      <>
        <p className="text-sm font-semibold" data-testid="summary">
          {files.length} {files.length === 1 ? "file" : "files"} changed, +{adds} -{dels}
        </p>
        {files.map((f) => (
          <FileView key={f.path} file={f} />
        ))}
      </>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      {back}
      <h1 className="text-xl font-semibold">Compare branches</h1>
      {branchesQ.data && (
        <div className="flex flex-wrap items-center gap-3">
          <RefSelect label="Base" value={base} branches={branchesQ.data.branches} onChange={(v) => set("base", v)} />
          <RefSelect label="Compare" value={head} branches={branchesQ.data.branches} onChange={(v) => set("head", v)} />
        </div>
      )}
      {body}
    </div>
  )
}
