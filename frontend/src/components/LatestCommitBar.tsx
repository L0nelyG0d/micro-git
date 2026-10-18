import { Link } from "react-router-dom"
import { OidLink } from "@/components/OidLink"
import { firstLine, formatDate } from "@/lib/format"
import type { CommitSummary } from "@/lib/git"

export function LatestCommitBar({
  owner,
  repo,
  commit,
  historyRef,
}: {
  owner: string
  repo: string
  commit: CommitSummary
  historyRef: string
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md bg-card p-3 text-sm border border-border">
      <span className="min-w-0 basis-full font-medium break-all sm:basis-auto">{commit.author}</span>
      <span className="min-w-0 basis-full break-words sm:flex-1 sm:basis-0">{firstLine(commit.message)}</span>
      <OidLink
        oid={commit.oid}
        to={`/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/commit/${commit.oid}`}
        copyable
      />
      <span className="text-xs whitespace-nowrap text-muted-foreground">{formatDate(commit.time)}</span>
      <Link
        to={`/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/commits/${encodeURIComponent(historyRef)}`}
        className="text-xs text-primary hover:underline"
      >
        History
      </Link>
    </div>
  )
}
