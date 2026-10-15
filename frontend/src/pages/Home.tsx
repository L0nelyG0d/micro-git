import { useCallback, useEffect, useState } from "react"
import { Link } from "react-router-dom"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { useAuth } from "@/auth/AuthContext"
import { NewRepoForm } from "@/components/NewRepoForm"
import { RepoTable } from "@/components/RepoTable"
import { api, ApiError } from "@/lib/api"
import type { Repo } from "@/lib/repos"

export function Home() {
  const { user, loading: sessionLoading, expireSession } = useAuth()
  const [repos, setRepos] = useState<Repo[] | null>(null)
  const [error, setError] = useState("")
  const [formOpen, setFormOpen] = useState(false)
  const [attempt, setAttempt] = useState(0)

  const load = useCallback(() => {
    setRepos(null)
    setError("")
    setAttempt((n) => n + 1)
  }, [])

  useEffect(() => {
    let active = true
    api<{ repos: Repo[] }>("/repos")
      .then((d) => active && setRepos(d.repos))
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
  }, [attempt])

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
            setRepos((r) => [repo, ...(r ?? [])])
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
      ) : repos === null ? (
        <div role="status" aria-label="Loading repositories" className="flex flex-col gap-2">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
        </div>
      ) : repos.length === 0 ? (
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
        <RepoTable repos={repos} />
      )}
    </div>
  )
}
