import type { ReactNode } from "react"
import { ArrowLeft, ArrowUp } from "lucide-react"
import { Link, useNavigate } from "react-router-dom"
import { Button } from "@/components/ui/button"

/**
 * Row under the top bar on inner pages: Back (browser history, or the parent
 * when the page was opened directly), Up (the parent page) and the breadcrumbs.
 */
export function PageToolbar({
  up,
  children,
}: {
  /** The parent page: where Up goes and where Back falls back to. */
  up?: { to: string; label: string }
  children?: ReactNode
}) {
  const navigate = useNavigate()
  // React Router stores the history position in history.state.idx.
  const hasHistory = ((window.history.state as { idx?: number } | null)?.idx ?? 0) > 0
  const goBack = () => (hasHistory ? navigate(-1) : up && navigate(up.to))
  return (
    <div
      role="toolbar"
      aria-label="Page navigation"
      className="flex flex-wrap items-center gap-2 rounded-xl bg-card p-2 shadow-xs ring-1 ring-border"
    >
      <Button type="button" variant="outline" size="sm" onClick={goBack} disabled={!hasHistory && !up}>
        <ArrowLeft aria-hidden />
        Back
      </Button>
      {up && (
        <Button asChild variant="outline" size="sm">
          <Link to={up.to} title={`Up to ${up.label}`}>
            <ArrowUp aria-hidden />
            Up
          </Link>
        </Button>
      )}
      {children && <div className="ml-1 min-w-0 flex-1 border-l border-border pl-3">{children}</div>}
    </div>
  )
}
