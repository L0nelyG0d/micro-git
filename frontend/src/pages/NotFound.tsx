import { Link } from "react-router-dom"

export function NotFound() {
  return (
    <div className="flex flex-col gap-3">
      <h1 className="text-xl font-semibold">Page not found</h1>
      <p className="text-sm text-muted-foreground">
        The page you are looking for does not exist.{" "}
        <Link to="/" className="text-primary underline underline-offset-2">
          Go home
        </Link>
      </p>
    </div>
  )
}
