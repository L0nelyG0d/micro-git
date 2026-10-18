import { Suspense, useEffect, useRef, useState } from "react"
import type { RefObject } from "react"
import { matchPath, Outlet, useLocation } from "react-router-dom"
import { Toaster } from "sonner"
import { AuthProvider, useAuth } from "@/auth/AuthContext"
import { ErrorBoundary } from "@/components/ErrorBoundary"
import { Nav } from "@/components/Nav"
import { LoadingBlock } from "@/components/StateViews"

const SUFFIX = "micro git"
const R = "/:owner/:repo"

function dec(s: string) {
  try {
    return decodeURIComponent(s)
  } catch {
    return s
  }
}

function titleFor(pathname: string): string {
  const m = (pattern: string) => matchPath(pattern, pathname)
  const repoOf = (p: { owner?: string; repo?: string }) => `${dec(p.owner ?? "")}/${dec(p.repo ?? "")}`
  const withRepo = (what: string, p: { owner?: string; repo?: string }) =>
    `${what} · ${repoOf(p)} · ${SUFFIX}`
  if (pathname === "/") return SUFFIX
  if (m("/login")) return `Log in · ${SUFFIX}`
  if (m("/register")) return `Register · ${SUFFIX}`
  const profile = m("/:owner")
  if (profile) return `${dec(profile.params.owner ?? "")} · ${SUFFIX}`
  const repo = m(R) ?? m(`${R}/tree/:ref/*`)
  if (repo) return `${repoOf(repo.params)} · ${SUFFIX}`
  const hist = m(`${R}/commits/:ref`)
  if (hist) return withRepo("History", hist.params)
  const commit = m(`${R}/commit/:oid`)
  if (commit) return withRepo(`Commit ${dec(commit.params.oid ?? "").slice(0, 7)}`, commit.params)
  const settings = m(`${R}/settings`)
  if (settings) return withRepo("Settings", settings.params)
  const cmp = m(`${R}/compare`)
  if (cmp) return withRepo("Compare", cmp.params)
  const nw = m(`${R}/new/:ref`)
  if (nw) return withRepo("New file", nw.params)
  const file = m(`${R}/blob/:ref/*`) ?? m(`${R}/edit/:ref/*`)
  if (file) return withRepo(dec(file.params["*"] ?? "").replace(/\/+$/, ""), file.params)
  return `Page not found · ${SUFFIX}`
}

/** Sets document.title from the route and moves focus to <main> after navigation. */
function TitleSync({ mainRef }: { mainRef: RefObject<HTMLElement | null> }) {
  const { pathname } = useLocation()
  // Compare with the previous pathname (not a boolean flag) so StrictMode's
  // double-invoked effect does not count as a navigation.
  const prev = useRef(pathname)
  useEffect(() => {
    document.title = titleFor(pathname)
    if (prev.current === pathname) return
    prev.current = pathname
    // Do not steal focus from an open dialog.
    if (document.querySelector('[role="dialog"], [role="alertdialog"]')) return
    mainRef.current?.focus()
  }, [pathname, mainRef])
  return null
}

/**
 * Pages remount when the logged-in user changes, so everything on screen is
 * fetched again: private repositories must appear after login and disappear
 * after logout.
 */
function Pages() {
  const { user, loading } = useAuth()
  const { pathname } = useLocation()
  // The first answer of /auth/me is not a change (nothing was fetched for
  // another user yet); only a later switch bumps the epoch and remounts.
  const [seen, setSeen] = useState<{ id: number | null } | null>(null)
  const [epoch, setEpoch] = useState(0)
  const id = user?.id ?? null
  if (!loading && seen?.id !== id) {
    setSeen({ id })
    if (seen) setEpoch((e) => e + 1)
  }
  return (
    <ErrorBoundary resetKey={pathname}>
      <Suspense fallback={<LoadingBlock label="Loading page" />} key={epoch}>
        <Outlet />
      </Suspense>
    </ErrorBoundary>
  )
}

/** Layout route (see routes.tsx): main.tsx mounts it in a data router (createBrowserRouter), tests in a memory data router. */
export default function App() {
  const mainRef = useRef<HTMLElement>(null)
  return (
    <AuthProvider>
      <TitleSync mainRef={mainRef} />
      <a
        href="#main"
        onClick={(e) => {
          e.preventDefault()
          mainRef.current?.focus()
        }}
        className="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-50 focus:rounded-md focus:bg-card focus:px-3 focus:py-2 focus:text-sm focus:ring-2 focus:ring-ring"
      >
        Skip to main content
      </a>
      <Nav />
      <main id="main" tabIndex={-1} ref={mainRef} className="mx-auto max-w-6xl p-4 outline-none md:py-6">
        <Pages />
      </main>
      <Toaster theme="system" />
    </AuthProvider>
  )
}
