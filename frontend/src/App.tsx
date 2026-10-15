import { Route, Routes } from "react-router-dom"
import { Toaster } from "sonner"
import { AuthProvider } from "@/auth/AuthContext"
import { Nav } from "@/components/Nav"
import { BlobPage } from "@/pages/BlobPage"
import { CommitPage } from "@/pages/CommitPage"
import { HistoryPage } from "@/pages/HistoryPage"
import { Home } from "@/pages/Home"
import { Login } from "@/pages/Login"
import { NotFound } from "@/pages/NotFound"
import { Placeholder } from "@/pages/Placeholder"
import { Register } from "@/pages/Register"
import { RepoPage } from "@/pages/RepoPage"

/** Router-agnostic: main.tsx wraps it in BrowserRouter, tests in MemoryRouter. */
export default function App() {
  return (
    <AuthProvider>
      <Nav />
      <main className="mx-auto max-w-4xl p-4">
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/register" element={<Register />} />
          <Route path="/" element={<Home />} />
          <Route path="/:owner/:repo" element={<RepoPage />} />
          <Route path="/:owner/:repo/tree/:ref/*" element={<RepoPage />} />
          <Route path="/:owner/:repo/blob/:ref/*" element={<BlobPage />} />
          <Route path="/:owner/:repo/edit/:ref/*" element={<Placeholder name="Edit file" />} />
          <Route path="/:owner/:repo/new/:ref" element={<Placeholder name="New file" />} />
          <Route path="/:owner/:repo/commits/:ref" element={<HistoryPage />} />
          <Route path="/:owner/:repo/commit/:oid" element={<CommitPage />} />
          <Route path="*" element={<NotFound />} />
        </Routes>
      </main>
      <Toaster theme="system" />
    </AuthProvider>
  )
}
