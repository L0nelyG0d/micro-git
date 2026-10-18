import { Link, useParams, useSearchParams } from "react-router-dom"
import { ErrorBlock, LoadingBlock, NotFoundBlock } from "@/components/StateViews"
import { PageToolbar } from "@/components/PageToolbar"
import { OidLink } from "@/components/OidLink"
import { Pagination } from "@/components/Pagination"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { ApiError } from "@/services"
import { firstLine, formatDate } from "@/lib/format"
import { commitPath, historyPath, isOidRef, uiPath, type CommitsPage } from "@/lib/git"
import type { Repo } from "@/lib/repos"
import { parsePage } from "@/lib/pagination"
import { useFetch } from "@/lib/useFetch"

/** `/:owner/:repo/commits/:ref?page=N` */
export function HistoryPage() {
  const { owner = "", repo: name = "", ref = "" } = useParams()
  const [search] = useSearchParams()
  const page = parsePage(search.get("page"))
  const base = `/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`

  const repoQ = useFetch<Repo>(base)
  // The ref is in the URL, so the history does not wait for the repo request.
  const q = useFetch<CommitsPage>(`${base}/commits?ref=${encodeURIComponent(ref)}&page=${page}`)

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
  const toolbar = (
    <PageToolbar up={{ to: uiPath("tree", owner, name, ref, ""), label: "files" }}>
      <span className="text-sm font-semibold">History</span>
    </PageToolbar>
  )
  const heading = (
    <div className="flex flex-col gap-1">
      <h1 className="text-xl font-semibold break-all">
        {repo.owner}/{repo.name}
      </h1>
      <p className="text-xs text-muted-foreground">
        History of{" "}
        <span className="font-mono max-sm:break-all">{isOidRef(ref) ? ref.slice(0, 7) : ref}</span>
        {isOidRef(ref) && " (read-only)"}
      </p>
    </div>
  )

  if (repo.empty) {
    return (
      <div className="flex flex-col gap-3">
        {toolbar}
        {heading}
        <p className="rounded-md bg-card p-3 text-sm text-muted-foreground shadow-xs ring-1 ring-border">
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
      <div className="flex flex-col items-start gap-2 rounded-md bg-card p-3 text-sm shadow-xs ring-1 ring-border">
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
      <div className="overflow-x-auto rounded-md border border-border bg-card">
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
      {toolbar}
      {heading}
      {body}
      <Pagination
        page={page}
        hasMore={hasMore}
        prevTo={historyPath(owner, name, ref, page - 1)}
        nextTo={historyPath(owner, name, ref, page + 1)}
      />
    </div>
  )
}
