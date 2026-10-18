import { lazy } from "react"
import type { RouteObject } from "react-router-dom"
import { Home } from "@/pages/Home"
import { NotFound } from "@/pages/NotFound"

const BlobPage = lazy(() => import("@/pages/BlobPage").then((m) => ({ default: m.BlobPage })))
const ComparePage = lazy(() => import("@/pages/ComparePage").then((m) => ({ default: m.ComparePage })))
const EditorPage = lazy(() => import("@/pages/EditorPage").then((m) => ({ default: m.EditorPage })))
const CommitPage = lazy(() => import("@/pages/CommitPage").then((m) => ({ default: m.CommitPage })))
const HistoryPage = lazy(() => import("@/pages/HistoryPage").then((m) => ({ default: m.HistoryPage })))
const Login = lazy(() => import("@/pages/Login").then((m) => ({ default: m.Login })))
const Register = lazy(() => import("@/pages/Register").then((m) => ({ default: m.Register })))
const RepoPage = lazy(() => import("@/pages/RepoPage").then((m) => ({ default: m.RepoPage })))
const ProfilePage = lazy(() => import("@/pages/ProfilePage").then((m) => ({ default: m.ProfilePage })))
const SettingsPage = lazy(() => import("@/pages/SettingsPage").then((m) => ({ default: m.SettingsPage })))

/**
 * The pages, as children of the App layout route. The app runs in a data router
 * (createBrowserRouter) because the editor's unsaved-changes guard needs useBlocker.
 */
export const pageRoutes: RouteObject[] = [
  { path: "/login", element: <Login /> },
  { path: "/register", element: <Register /> },
  { path: "/", element: <Home /> },
  { path: "/:owner", element: <ProfilePage /> },
  { path: "/:owner/:repo", element: <RepoPage /> },
  { path: "/:owner/:repo/tree/:ref/*", element: <RepoPage /> },
  { path: "/:owner/:repo/blob/:ref/*", element: <BlobPage /> },
  { path: "/:owner/:repo/edit/:ref/*", element: <EditorPage mode="edit" /> },
  { path: "/:owner/:repo/new/:ref", element: <EditorPage mode="new" /> },
  { path: "/:owner/:repo/commits/:ref", element: <HistoryPage /> },
  { path: "/:owner/:repo/commit/:oid", element: <CommitPage /> },
  { path: "/:owner/:repo/settings", element: <SettingsPage /> },
  { path: "/:owner/:repo/compare", element: <ComparePage /> },
  { path: "*", element: <NotFound /> },
]
