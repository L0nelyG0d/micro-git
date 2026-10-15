import { Link } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { errorMessage } from "@/lib/useFetch"

export function LoadingBlock({ label }: { label: string }) {
  return (
    <div role="status" aria-label={label} className="flex flex-col gap-2">
      <Skeleton className="h-6 w-1/3" />
      <Skeleton className="h-8 w-full" />
      <Skeleton className="h-8 w-full" />
      <Skeleton className="h-8 w-full" />
    </div>
  )
}

export function ErrorBlock({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  return (
    <div role="alert" className="flex flex-col items-start gap-2 text-sm">
      <p className="text-destructive">{errorMessage(error)}</p>
      <Button variant="outline" size="sm" onClick={onRetry}>
        Retry
      </Button>
    </div>
  )
}

export function NotFoundBlock({ title, hint }: { title: string; hint: string }) {
  return (
    <div className="flex flex-col items-start gap-1" data-testid="not-found">
      <h1 className="text-xl font-semibold">{title}</h1>
      <p className="text-sm text-muted-foreground">{hint}</p>
      <Link to="/" className="text-sm text-primary underline underline-offset-2">
        Back to repositories
      </Link>
    </div>
  )
}
