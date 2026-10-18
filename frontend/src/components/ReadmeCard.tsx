import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import type { Blob } from "@/lib/git"
import { renderMarkdown } from "@/lib/markdown"
import type { FetchState } from "@/lib/useFetch"

/**
 * README.md of the repo root. The page fetches it alongside the tree and
 * renders this card only when the tree lists the file.
 */
export function ReadmeCard({ q }: { q: FetchState<Blob> }) {
  return (
    <Card className="gap-2 py-3" data-testid="readme">
      <h2 className="px-3 text-sm font-semibold">README.md</h2>
      <div className="px-3 text-sm">
        {q.error ? (
          <div role="alert" className="flex items-center gap-2 text-muted-foreground">
            <span>Could not load the README.</span>
            <Button variant="outline" size="sm" onClick={q.reload}>
              Retry
            </Button>
          </div>
        ) : !q.data ? (
          <div role="status" aria-label="Loading README" className="flex flex-col gap-2">
            <Skeleton className="h-4 w-1/3" />
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-full" />
          </div>
        ) : (
          <div className="flex flex-col gap-3">{renderMarkdown(q.data.content)}</div>
        )}
      </div>
    </Card>
  )
}
