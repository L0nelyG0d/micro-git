import { useCallback, useEffect, useState } from "react"
import { Link, useSearchParams } from "react-router-dom"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { useAuth } from "@/auth/AuthContext"
import { NewRepoForm } from "@/components/NewRepoForm"
import { Pagination } from "@/components/Pagination"
import { Section } from "@/components/Section"
import { RepoTable } from "@/components/RepoTable"
import { api, ApiError } from "@/lib/api"
import { parsePage } from "@/lib/pagination"
import type { RepoList } from "@/lib/repos"

export function Home() {
  const { user, loading: sessionLoading, expireSession } = useAuth()
  const [search] = useSearchParams()
  const page = parsePage(search.get("page"))
  const [list, setList] = useState<RepoList | null>(null)
  const [error, setError] = useState("")
  const [formOpen, setFormOpen] = useState(false)
  const [attempt, setAttempt] = useState(0)

  const load = useCallback(() => {
    setList(null)
    setError("")
    setAttempt((n) => n + 1)
  }, [])

  useEffect(() => {
    let active = true
    setList(null)
    setError("")
    api<RepoList>(`/repos?page=${page}`)
      .then((d) => active && setList(d))
      .catch((e) => {
        if (!active) return
        setError(
          e instanceof ApiError
            ? e.message
            : "Could not reach the server. Check your connection and try again.",
        )
      })
    return () => {
      active = false
    }
  }, [attempt, page])

  // Stays open after a 401 so the expiry message remains visible.
  const showForm = formOpen

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">Repositories</h1>
        {sessionLoading ? null : user ? (
          !showForm && <Button onClick={() => setFormOpen(true)}>New repository</Button>
        ) : (
          <Link to="/login" state={{ from: "/" }} className="text-sm text-primary underline underline-offset-2">
            Log in to create a repository
          </Link>
        )}
      </div>

      {showForm && (
        <NewRepoForm
          onCancel={() => setFormOpen(false)}
          onUnauthorized={expireSession}
          onCreated={(repo) => {
            // A new repo is the newest, so it belongs on page 1 only.
            if (page === 1)
              setList((l) => {
                if (!l) return l
                const all = [repo, ...l.repos]
                // Keep the page size; the pushed-out repo moves to page 2.
                return { ...l, repos: all.slice(0, l.per_page), has_more: l.has_more || all.length > l.per_page }
              })
            setFormOpen(false)
            toast.success(`Repository ${repo.owner}/${repo.name} created.`)
          }}
        />
      )}

      {error ? (
        <div role="alert" className="flex flex-col items-start gap-2 text-sm">
          <p className="text-destructive">{error}</p>
          <Button variant="outline" size="sm" onClick={load}>
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
        <Section title="Repository list" className="[&>div]:p-0">
          <RepoTable repos={list.repos} />
        </Section>
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
