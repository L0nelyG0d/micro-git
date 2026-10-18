import { Link, useLocation, useNavigate } from "react-router-dom"
import { useState } from "react"
import { toast } from "sonner"
import { GitBranch, TriangleAlert } from "lucide-react"
import { Avatar } from "@/components/Avatar"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { useAuth } from "@/auth/AuthContext"

export function Nav() {
  const { user, loading, sessionError, logout } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const [busy, setBusy] = useState(false)

  async function onLogout() {
    setBusy(true)
    try {
      await logout()
      navigate("/")
    } catch {
      toast.error("Could not log out. Check your connection and try again.")
    } finally {
      setBusy(false)
    }
  }

  return (
    <header className="sticky top-0 z-10 border-b border-border bg-header text-header-foreground">
      <nav aria-label="Main" className="mx-auto flex h-14 max-w-6xl items-center justify-between gap-3 px-4">
        <Link to="/" className="flex items-center gap-2 font-semibold">
          <GitBranch className="size-6" aria-hidden />
          micro git
        </Link>
        <div className="flex items-center gap-2 text-sm">
          {sessionError && (
            <span
              role="status"
              title="Could not check your session. Public pages still work."
              className="flex items-center gap-1 text-xs text-destructive"
            >
              <TriangleAlert className="size-4" aria-hidden />
              Session unavailable
            </span>
          )}
          {loading ? (
            <Skeleton aria-label="Loading session" role="status" className="h-6 w-28" />
          ) : user ? (
            <>
              <Link
                to={`/${encodeURIComponent(user.username)}`}
                title={`${user.username}'s repositories`}
                className="flex items-center gap-2 rounded-md px-1 py-1 hover:bg-accent"
              >
                <Avatar name={user.username} />
                <span className="max-w-24 truncate">{user.username}</span>
              </Link>
              <Button variant="outline" size="sm" className="shrink-0" onClick={onLogout} disabled={busy}>
                Log out
              </Button>
            </>
          ) : (
            <>
              <Button variant="outline" size="sm" asChild>
                <Link to="/login" state={{ from: location.pathname + location.search }}>
                  Log in
                </Link>
              </Button>
              <Button size="sm" asChild>
                <Link to="/register">Register</Link>
              </Button>
            </>
          )}
        </div>
      </nav>
    </header>
  )
}
