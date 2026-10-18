import { useState } from "react"
import { Link } from "react-router-dom"
import { RepoBadge } from "@/components/RepoBadge"
import { Input } from "@/components/ui/input"
import { formatDate } from "@/lib/format"
import type { Repo } from "@/lib/repos"

/** GitHub-style repository rows with a "Find a repository…" filter. `showOwner` prints owner/name instead of name. */
export function RepoRows({ repos, showOwner = true }: { repos: Repo[]; showOwner?: boolean }) {
  const [query, setQuery] = useState("")
  const q = query.trim().toLowerCase()
  const shown = q
    ? repos.filter((r) => `${r.owner}/${r.name} ${r.description}`.toLowerCase().includes(q))
    : repos
  return (
    <div className="flex flex-col">
      <Input
        type="search"
        aria-label="Find a repository"
        placeholder="Find a repository…"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        className="mb-2"
      />
      {shown.length === 0 ? (
        <p className="border-t border-border py-6 text-center text-sm text-muted-foreground">
          No repositories match “{query}”.
        </p>
      ) : (
        <ul aria-label="Repository list" className="flex flex-col border-t border-border">
          {shown.map((r) => (
            <li key={r.id} className="flex flex-col gap-1 border-b border-border py-4">
              <div className="flex flex-wrap items-center gap-2">
                <Link
                  to={`/${encodeURIComponent(r.owner)}/${encodeURIComponent(r.name)}`}
                  className="text-lg font-semibold break-all text-primary hover:underline"
                >
                  {showOwner ? `${r.owner}/${r.name}` : r.name}
                </Link>
                <RepoBadge isPrivate={r.private} />
              </div>
              {r.description && <p className="text-sm break-words text-muted-foreground">{r.description}</p>}
              <p className="text-xs text-muted-foreground">
                Created <time dateTime={r.created_at}>{formatDate(r.created_at)}</time>
              </p>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
