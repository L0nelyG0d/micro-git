import { useEffect, useRef, useState } from "react"
import { Link, useSearchParams } from "react-router-dom"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { useAuth } from "@/auth/AuthContext"
import { NewRepoForm } from "@/components/NewRepoForm"
import { Pagination } from "@/components/Pagination"
import { RepoRows } from "@/components/RepoRows"
import { cacheClear } from "@/lib/fetchCache"
import { errorMessage, useFetch } from "@/lib/useFetch"
import { parsePage } from "@/lib/pagination"
import type { RepoList } from "@/lib/repos"

export function Home() {
  const { user, loading: sessionLoading, expireSession } = useAuth()
  const [search] = useSearchParams()
  const page = parsePage(search.get("page"))
  const [formOpen, setFormOpen] = useState(false)

  // Wait for the session: the list differs for a logged-in user (private repos).
  const { data, error, reload } = useFetch<RepoList>(sessionLoading ? null : `/repos?page=${page}`)
  // A repo created here is shown at once; it applies to the fetched `data` it was
  // built from only, so newer data (reload, revalidation) replaces it.
  const [created, setCreated] = useState<{ base: RepoList; list: RepoList }>()
  const list = created && created.base === data ? created.list : (data ?? null)

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

  // Stays open after a 401 so the expiry message remains visible.

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">Repositories</h1>
        {sessionLoading ? null : user ? (
          !formOpen && <Button onClick={() => setFormOpen(true)}>New repository</Button>
        ) : (
          <Link to="/login" state={{ from: "/" }} className="text-sm text-primary underline underline-offset-2">
            Log in to create a repository
          </Link>
        )}
      </div>

      {formOpen && (
        <NewRepoForm
          onCancel={() => setFormOpen(false)}
          onUnauthorized={expireSession}
          onCreated={(repo) => {
            // A new repo is the newest, so it belongs on page 1 only.
            if (page === 1 && data) {
              const all = [repo, ...data.repos]
              // Keep the page size; the pushed-out repo moves to page 2.
              setCreated({
                base: data,
                list: { ...data, repos: all.slice(0, data.per_page), has_more: data.has_more || all.length > data.per_page },
              })
            }
            setFormOpen(false)
            toast.success(`Repository ${repo.owner}/${repo.name} created.`)
          }}
        />
      )}

      {error ? (
        <div role="alert" className="flex flex-col items-start gap-2 text-sm">
          <p className="text-destructive">{errorMessage(error)}</p>
          <Button variant="outline" size="sm" onClick={reload}>
            Retry
          </Button>
        </div>
      ) : list === null ? (
        <div role="status" aria-label="Loading repositories" className="flex flex-col gap-2">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
        </div>
      ) : list.repos.length === 0 && page > 1 ? (
        <div className="flex flex-col items-start gap-2 text-sm">
          <p className="text-muted-foreground">There are no more repositories on this page.</p>
          <Link to="/" className="text-primary underline underline-offset-2">
            Go to the first page
          </Link>
        </div>
      ) : list.repos.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No repositories yet.{" "}
          {user ? (
            "Use New repository to create the first one."
          ) : (
            <>
              <Link to="/login" state={{ from: "/" }} className="text-primary underline underline-offset-2">
                Log in to create the first one
              </Link>
              .
            </>
          )}
        </p>
      ) : (
        <RepoRows repos={list.repos} />
      )}

      {list && (page > 1 || list.has_more) && (
        <Pagination
          page={page}
          hasMore={list.has_more}
          prevTo={page - 1 > 1 ? `/?page=${page - 1}` : "/"}
          nextTo={`/?page=${page + 1}`}
        />
      )}
    </div>
  )
}
