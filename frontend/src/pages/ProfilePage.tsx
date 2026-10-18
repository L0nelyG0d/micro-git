import { useEffect, useRef } from "react"
import { Link, useParams, useSearchParams } from "react-router-dom"
import { Avatar } from "@/components/Avatar"
import { Pagination } from "@/components/Pagination"
import { RepoRows } from "@/components/RepoRows"
import { ErrorBlock, LoadingBlock } from "@/components/StateViews"
import { Button } from "@/components/ui/button"
import { isOwner, useAuth } from "@/auth/AuthContext"
import { cacheClear } from "@/lib/fetchCache"
import { useFetch } from "@/lib/useFetch"
import { parsePage } from "@/lib/pagination"
import type { RepoList as RepoListData } from "@/lib/repos"

/** `/:owner`: a user's repositories, like a GitHub profile's Repositories tab. Private ones show for their owner only. */
export function ProfilePage() {
  const { owner = "" } = useParams()
  const { user, loading: sessionLoading } = useAuth()
  const [search] = useSearchParams()
  const page = parsePage(search.get("page"))
  const ownerPath = encodeURIComponent(owner)
  const { data: list, error, reload } = useFetch<RepoListData>(
    sessionLoading ? null : `/repos?owner=${ownerPath}&page=${page}`,
  )

  // The cache is keyed by path, not by user: when the user changes, drop it and refetch.
  const seenUser = useRef<{ id?: number } | undefined>(undefined)
  useEffect(() => {
    if (sessionLoading) return
    if (seenUser.current && seenUser.current.id !== user?.id) {
      cacheClear()
      reload()
    }
    seenUser.current = { id: user?.id }
  }, [user?.id, sessionLoading, reload])

  const mine = isOwner(user, owner)
  return (
    <div className="grid gap-6 md:grid-cols-[16rem_minmax(0,1fr)]">
      <aside className="flex flex-col gap-3">
        <Avatar name={owner} className="size-40 text-6xl md:size-64 md:text-8xl" />
        <h1 className="text-2xl font-semibold break-all">{owner}</h1>
        {mine && (
          <Button asChild variant="outline" size="sm">
            <Link to="/">Create a repository</Link>
          </Button>
        )}
      </aside>
      <section aria-label={`Repositories of ${owner}`} className="flex min-w-0 flex-col gap-3">
        <h2 className="border-b border-border pb-2 text-base font-semibold">Repositories</h2>
        {error ? (
          <ErrorBlock error={error} onRetry={reload} />
        ) : !list ? (
          <LoadingBlock label="Loading repositories" />
        ) : list.repos.length === 0 ? (
          <p className="text-sm text-muted-foreground">{owner} has no repositories you can see.</p>
        ) : (
          <RepoRows repos={list.repos} showOwner={false} />
        )}
        {list && (page > 1 || list.has_more) && (
          <Pagination
            page={page}
            hasMore={list.has_more}
            prevTo={page - 1 > 1 ? `/${ownerPath}?page=${page - 1}` : `/${ownerPath}`}
            nextTo={`/${ownerPath}?page=${page + 1}`}
          />
        )}
      </section>
    </div>
  )
}
