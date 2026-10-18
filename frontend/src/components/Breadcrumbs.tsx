import { Link } from "react-router-dom"
import { uiPath } from "@/lib/git"

/** Repo name, then each path segment, each linking back to that directory. */
export function Breadcrumbs({
  owner,
  repo,
  ref_,
  path,
  lastIsFile,
}: {
  owner: string
  repo: string
  ref_: string
  path: string
  /** The last segment is a file: shown as plain text, not a link. */
  lastIsFile?: boolean
}) {
  const segs = path ? path.split("/") : []
  // A long unbroken name may shrink to the row and break anywhere instead of
  // widening the page. Names that fit are untouched: the row wraps first.
  const shrink = "min-w-0 break-all"
  const linkClass = `text-primary hover:underline ${shrink}`
  return (
    <nav aria-label="Breadcrumbs" className="flex flex-wrap items-center gap-1 text-sm min-w-0">
      <Link to={uiPath("tree", owner, repo, ref_, "")} className={`${linkClass} font-semibold`}>
        {repo}
      </Link>
      {segs.map((seg, i) => {
        const last = i === segs.length - 1
        const to = uiPath("tree", owner, repo, ref_, segs.slice(0, i + 1).join("/"))
        return (
          <span key={i} className="flex items-center gap-1 min-w-0">
            <span aria-hidden className="text-muted-foreground">
              /
            </span>
            {last && lastIsFile ? (
              <span aria-current="page" className={`font-semibold ${shrink}`}>
                {seg}
              </span>
            ) : (
              <Link to={to} className={linkClass}>
                {seg}
              </Link>
            )}
          </span>
        )
      })}
    </nav>
  )
}
