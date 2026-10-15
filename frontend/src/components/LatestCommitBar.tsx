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
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-xl bg-card p-3 text-sm shadow-xs ring-1 ring-border">
      <span className="font-medium">{commit.author}</span>
      <span className="min-w-0 flex-1 break-words">{firstLine(commit.message)}</span>
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
