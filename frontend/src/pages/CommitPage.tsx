import { Link, useParams } from "react-router-dom"
import { ErrorBlock, LoadingBlock, NotFoundBlock } from "@/components/StateViews"
import { OidLink } from "@/components/OidLink"
import { ApiError } from "@/lib/api"
import { formatDate } from "@/lib/format"
import { FileView } from "@/components/DiffView"
import { commitPath, uiPath, type CommitDetail } from "@/lib/git"
import { useFetch } from "@/lib/useFetch"

/** `/:owner/:repo/commit/:oid` */
export function CommitPage() {
  const { owner = "", repo = "", oid = "" } = useParams()
  const q = useFetch<CommitDetail>(
    `/repos/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/commits/${encodeURIComponent(oid)}`,
  )

  if (q.error instanceof ApiError && [400, 404].includes(q.error.status)) {
    return (
      <NotFoundBlock
        title="Commit not found"
        hint={`${q.error.message}. Check the address, or browse the history to find the commit.`}
      />
    )
  }
  if (q.error) return <ErrorBlock error={q.error} onRetry={q.reload} />
  if (!q.data) return <LoadingBlock label="Loading commit" />

  const c = q.data
  const adds = c.files.reduce((s, f) => s + f.additions, 0)
  const dels = c.files.reduce((s, f) => s + f.deletions, 0)
  const count = c.files.length

  return (
    <div className="flex flex-col gap-3">
      <Link to={uiPath("tree", owner, repo, c.oid, "")} className="text-sm text-primary hover:underline">
        {owner}/{repo}
      </Link>
      <div className="flex flex-col gap-1 rounded-xl bg-card p-3 shadow-xs ring-1 ring-border">
        <h1 className="text-xl font-semibold whitespace-pre-wrap break-words">{c.message}</h1>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
          <span>{c.author}</span>
          <span>{formatDate(c.time)}</span>
          <span className="inline-flex items-center gap-1">
            commit <OidLink oid={c.oid} to={commitPath(owner, repo, c.oid)} copyable />
          </span>
          <span className="inline-flex items-center gap-1">
            {c.parents.length === 0 ? (
              "root commit"
            ) : (
              <>
                {c.parents.length > 1 ? "parents" : "parent"}
                {c.parents.map((p) => (
                  <OidLink key={p} oid={p} to={commitPath(owner, repo, p)} />
                ))}
              </>
            )}
          </span>
        </div>
      </div>
      <p className="text-sm font-semibold" data-testid="summary">
        {count} {count === 1 ? "file" : "files"} changed, {adds} {adds === 1 ? "addition" : "additions"},{" "}
        {dels} {dels === 1 ? "deletion" : "deletions"}
      </p>
      {count === 0 ? (
        <p className="rounded-xl bg-card p-3 text-sm text-muted-foreground shadow-xs ring-1 ring-border">
          no files changed
        </p>
      ) : (
        c.files.map((f) => <FileView key={f.path} file={f} />)
      )}
    </div>
  )
}
