import { useEffect, useRef } from "react"
import type { RefObject } from "react"
import { matchPath, Route, Routes, useLocation } from "react-router-dom"
import { Toaster } from "sonner"
import { AuthProvider } from "@/auth/AuthContext"
import { Nav } from "@/components/Nav"
import { BlobPage } from "@/pages/BlobPage"
import { ComparePage } from "@/pages/ComparePage"
import { EditorPage } from "@/pages/EditorPage"
import { CommitPage } from "@/pages/CommitPage"
import { HistoryPage } from "@/pages/HistoryPage"
import { Home } from "@/pages/Home"
import { Login } from "@/pages/Login"
import { NotFound } from "@/pages/NotFound"
import { Register } from "@/pages/Register"
import { RepoPage } from "@/pages/RepoPage"

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
  const repo = m(R) ?? m(`${R}/tree/:ref/*`)
  if (repo) return `${repoOf(repo.params)} · ${SUFFIX}`
  const hist = m(`${R}/commits/:ref`)
  if (hist) return withRepo("History", hist.params)
  const commit = m(`${R}/commit/:oid`)
  if (commit) return withRepo(`Commit ${dec(commit.params.oid ?? "").slice(0, 7)}`, commit.params)
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

/** Router-agnostic: main.tsx wraps it in BrowserRouter, tests in MemoryRouter. */
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
      <main id="main" tabIndex={-1} ref={mainRef} className="mx-auto max-w-4xl p-4 outline-none">
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/register" element={<Register />} />
          <Route path="/" element={<Home />} />
          <Route path="/:owner/:repo" element={<RepoPage />} />
          <Route path="/:owner/:repo/tree/:ref/*" element={<RepoPage />} />
          <Route path="/:owner/:repo/blob/:ref/*" element={<BlobPage />} />
          <Route path="/:owner/:repo/edit/:ref/*" element={<EditorPage mode="edit" />} />
          <Route path="/:owner/:repo/new/:ref" element={<EditorPage mode="new" />} />
          <Route path="/:owner/:repo/commits/:ref" element={<HistoryPage />} />
          <Route path="/:owner/:repo/commit/:oid" element={<CommitPage />} />
          <Route path="/:owner/:repo/compare" element={<ComparePage />} />
          <Route path="*"element={<NotFound />} />
        </Routes>
      </main>
      <Toaster theme="system" />
    </AuthProvider>
  )
}
