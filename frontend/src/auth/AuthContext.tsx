import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react"
import type { ReactNode } from "react"
import { toast } from "sonner"
import { api, ApiError } from "@/lib/api"

export type User = { id: number; username: string }

type AuthState = {
  /** Current user, or null when logged out (or while loading). */
  user: User | null
  /** True until the first GET /auth/me has finished. */
  loading: boolean
  /** True when /auth/me failed with a network error or 5xx (not a 401). */
  sessionError: boolean
  login: (username: string, password: string) => Promise<User>
  logout: () => Promise<void>
  /** Marks the session as gone locally (after a 401 from any endpoint). */
  expireSession: () => void
}

const AuthContext = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUserState] = useState<User | null>(null)
  // Mirrors `user` synchronously so expireSession can tell, even when called twice in a row, whether a user was still logged in.
  const userRef = useRef<User | null>(null)
  const setUser = useCallback((u: User | null) => {
    userRef.current = u
    setUserState(u)
  }, [])
  const [loading, setLoading] = useState(true)
  const [sessionError, setSessionError] = useState(false)

  useEffect(() => {
    let active = true
    api<User>("/auth/me")
      .then((u) => active && setUser(u))
      .catch((e) => {
        if (!active) return
        if (!(e instanceof ApiError && e.status === 401)) setSessionError(true)
      })
      .finally(() => active && setLoading(false))
    return () => {
      active = false
    }
  }, [setUser])

  const login = useCallback(async (username: string, password: string) => {
    const u = await api<User>("/auth/login", { method: "POST", body: { username, password } })
    setUser(u)
    setSessionError(false)
    return u
  }, [setUser])

  const logout = useCallback(async () => {
    await api("/auth/logout", { method: "POST" })
    setUser(null)
  }, [setUser])

  const expireSession = useCallback(() => {
    if (!userRef.current) return
    setUser(null)
    toast.error("Your session has expired. Log in again.")
  }, [setUser])

  const value = useMemo(
    () => ({ user, loading, sessionError, login, logout, expireSession }),
    [user, loading, sessionError, login, logout, expireSession],
  )
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

// eslint-disable-next-line react-refresh/only-export-components
export function useAuth(): AuthState {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error("useAuth must be used inside AuthProvider")
  return ctx
}

/** Case-insensitive owner check: owner lookup in URLs is case-insensitive. */
// eslint-disable-next-line react-refresh/only-export-components
export function isOwner(user: User | null, owner: string | undefined): boolean {
  return !!user && !!owner && user.username.toLowerCase() === owner.toLowerCase()
}
