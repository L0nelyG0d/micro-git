import { Link, useParams, useSearchParams } from "react-router-dom"
import { ErrorBlock, LoadingBlock, NotFoundBlock } from "@/components/StateViews"
import { OidLink } from "@/components/OidLink"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { ApiError } from "@/lib/api"
import { firstLine, formatDate } from "@/lib/format"
import { commitPath, historyPath, isOidRef, uiPath, type CommitsPage } from "@/lib/git"
import type { Repo } from "@/lib/repos"
import { useFetch } from "@/lib/useFetch"

function parsePage(raw: string | null): number {
  const n = Number(raw)
  return Number.isInteger(n) && n >= 1 ? n : 1
}

/** `/:owner/:repo/commits/:ref?page=N` */
export function HistoryPage() {
  const { owner = "", repo: name = "", ref = "" } = useParams()
  const [search] = useSearchParams()
  const page = parsePage(search.get("page"))
  const base = `/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`

  const repoQ = useFetch<Repo>(base)
  const live = repoQ.data && !repoQ.data.empty
  const q = useFetch<CommitsPage>(
    live ? `${base}/commits?ref=${encodeURIComponent(ref)}&page=${page}` : null,
  )

  const repoMissing = repoQ.error instanceof ApiError && repoQ.error.status === 404
  if (repoMissing) {
    return (
      <NotFoundBlock
        title="Repository not found"
        hint={`There is no repository ${owner}/${name}. Check the address or go back to the list.`}
      />
    )
  }
  if (repoQ.error) return <ErrorBlock error={repoQ.error} onRetry={repoQ.reload} />
  if (!repoQ.data) return <LoadingBlock label="Loading history" />

  const repo = repoQ.data
  const back = (
    <Link to={uiPath("tree", owner, name, ref, "")} className="text-sm text-primary hover:underline">
      Back to files
    </Link>
  )
  const heading = (
    <div className="flex flex-col gap-1">
      <h1 className="text-xl font-semibold break-all">
        {repo.owner}/{repo.name}
      </h1>
      <p className="text-xs text-muted-foreground">
        History of{" "}
        <span className="font-mono">{isOidRef(ref) ? ref.slice(0, 7) : ref}</span>
        {isOidRef(ref) && " (read-only)"}
      </p>
    </div>
  )

  if (repo.empty) {
    return (
      <div className="flex flex-col gap-3">
        {heading}
        <p className="rounded-xl bg-card p-3 text-sm text-muted-foreground shadow-xs ring-1 ring-border">
          This repository is empty. Nothing has been committed yet.
        </p>
      </div>
    )
  }

  if (q.error instanceof ApiError && [400, 404].includes(q.error.status)) {
    return (
      <NotFoundBlock
        title="History not found"
        hint={`${q.error.message}. Check the address, or pick another branch.`}
      />
    )
  }

  let body
  if (q.error) {
    body = <ErrorBlock error={q.error} onRetry={q.reload} />
  } else if (!q.data) {
    body = <LoadingBlock label="Loading commits" />
  } else if (q.data.commits.length === 0) {
    body = (
      <div className="flex flex-col items-start gap-2 rounded-xl bg-card p-3 text-sm shadow-xs ring-1 ring-border">
        <p className="text-muted-foreground">
          {page > 1 ? "There are no more commits on this page." : "There are no commits here yet."}
        </p>
        {page > 1 && (
          <Link to={historyPath(owner, name, ref)} className="text-primary hover:underline">
            Go to the first page
          </Link>
        )}
      </div>
    )
  } else {
    body = (
      <div className="overflow-x-auto rounded-xl bg-card shadow-xs ring-1 ring-border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="px-2 text-xs text-muted-foreground">Message</TableHead>
              <TableHead className="px-2 text-xs text-muted-foreground">Author</TableHead>
              <TableHead className="px-2 text-xs text-muted-foreground">Date</TableHead>
              <TableHead className="px-2 text-xs text-muted-foreground">Commit</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {q.data.commits.map((c) => (
              <TableRow key={c.oid}>
                <TableCell className="px-2 py-1.5 text-sm whitespace-normal">{firstLine(c.message)}</TableCell>
                <TableCell className="px-2 py-1.5 text-sm">{c.author}</TableCell>
                <TableCell className="px-2 py-1.5 text-sm whitespace-nowrap">{formatDate(c.time)}</TableCell>
                <TableCell className="px-2 py-1.5">
                  <OidLink oid={c.oid} to={commitPath(owner, name, c.oid)} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    )
  }

  const hasMore = q.data?.has_more ?? false
  return (
    <div className="flex flex-col gap-3">
      {heading}
      {back}
      {body}
      <nav aria-label="Pagination" className="flex items-center justify-between gap-3">
        {page > 1 ? (
          <Button asChild variant="outline" size="sm">
            <Link to={historyPath(owner, name, ref, page - 1)}>Previous</Link>
          </Button>
        ) : (
          <Button variant="outline" size="sm" disabled>
            Previous
          </Button>
        )}
        <span className="text-xs text-muted-foreground">Page {page}</span>
        {hasMore ? (
          <Button asChild variant="outline" size="sm">
            <Link to={historyPath(owner, name, ref, page + 1)}>Next</Link>
          </Button>
        ) : (
          <Button variant="outline" size="sm" disabled>
            Next
          </Button>
        )}
      </nav>
    </div>
  )
}
