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
    <nav aria-label="Pagination" className="flex items-center justify-between gap-3">
      {page > 1 ? (
        <Button asChild variant="outline" size="sm">
          <Link to={prevTo}>Previous</Link>
        </Button>
      ) : (
        <Button variant="outline" size="sm" disabled>
          Previous
        </Button>
      )}
      <span className="text-xs text-muted-foreground">Page {page}</span>
      {hasMore ? (
        <Button asChild variant="outline" size="sm">
          <Link to={nextTo}>Next</Link>
        </Button>
      ) : (
        <Button variant="outline" size="sm" disabled>
          Next
        </Button>
      )}
    </nav>
  )
}
