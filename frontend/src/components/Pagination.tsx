import { ChevronLeft, ChevronRight } from "lucide-react"
import { Link } from "react-router-dom"
import { Button } from "@/components/ui/button"

/** Previous / "Page N" / Next controls; `prevTo` and `nextTo` are the targets for the enabled buttons. */
export function Pagination({
  page,
  hasMore,
  prevTo,
  nextTo,
}: {
  page: number
  hasMore: boolean
  prevTo: string
  nextTo: string
}) {
  return (
    <nav aria-label="Pagination" className="flex items-center justify-between gap-3 rounded-xl bg-card p-2 shadow-xs ring-1 ring-border">
      {page > 1 ? (
        <Button asChild variant="outline" size="sm">
          <Link to={prevTo}>
            <ChevronLeft aria-hidden />
            Previous
          </Link>
        </Button>
      ) : (
        <Button variant="outline" size="sm" disabled>
          <ChevronLeft aria-hidden />
          Previous
        </Button>
      )}
      <span className="text-xs text-muted-foreground">Page {page}</span>
      {hasMore ? (
        <Button asChild variant="outline" size="sm">
          <Link to={nextTo}>
            Next
            <ChevronRight aria-hidden />
          </Link>
        </Button>
      ) : (
        <Button variant="outline" size="sm" disabled>
          Next
          <ChevronRight aria-hidden />
        </Button>
      )}
    </nav>
  )
}
