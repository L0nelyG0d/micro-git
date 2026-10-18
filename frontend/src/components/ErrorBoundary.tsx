import { Component } from "react"
import type { ErrorInfo, ReactNode } from "react"
import { Link } from "react-router-dom"
import { Button } from "@/components/ui/button"

/** Same look as ErrorBlock: an alert with a Retry button, plus a way out. */
export function ErrorFallback({ onRetry }: { onRetry: () => void }) {
  return (
    <div role="alert" className="flex flex-col items-start gap-2">
      <h1 className="text-xl font-semibold">Something went wrong</h1>
      <p className="text-sm text-destructive">
        Check your connection and try again or go back to repositories.
      </p>
      <div className="flex items-center gap-3">
        <Button variant="outline" size="sm" onClick={onRetry}>
          Retry
        </Button>
        <Link to="/" className="text-sm text-primary underline underline-offset-2">
          Back to repositories
        </Link>
      </div>
    </div>
  )
}

type Props = {
  /** When this changes (e.g. the pathname) a shown fallback is cleared. */
  resetKey?: unknown
  children: ReactNode
}

/**
 * Catches errors thrown while rendering the pages (including a lazy chunk that
 * failed to load) and shows ErrorFallback in place, so the layout stays.
 * Retry renders the children again; a changed resetKey does the same.
 */
export class ErrorBoundary extends Component<Props, { failed: boolean }> {
  state = { failed: false }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error(error, info.componentStack)
  }

  componentDidUpdate(prev: Props) {
    if (this.state.failed && prev.resetKey !== this.props.resetKey) this.setState({ failed: false })
  }

  render() {
    if (this.state.failed) return <ErrorFallback onRetry={() => this.setState({ failed: false })} />
    return this.props.children
  }
}
