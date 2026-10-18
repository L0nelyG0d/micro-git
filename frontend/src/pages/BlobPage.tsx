import { Link, useParams } from "react-router-dom"
import { useAuth } from "@/auth/AuthContext"
import { PageToolbar } from "@/components/PageToolbar"
import { Breadcrumbs } from "@/components/Breadcrumbs"
import { ErrorBlock, LoadingBlock, NotFoundBlock } from "@/components/StateViews"
import { Button } from "@/components/ui/button"
import { ApiError } from "@/lib/api"
import { formatSize } from "@/lib/format"
import { canWrite, readPath, uiPath, type Blob } from "@/lib/git"
import { useFetch } from "@/lib/useFetch"

/** `/:owner/:repo/blob/:ref/*` */
export function BlobPage() {
  const { owner = "", repo = "", ref = "", "*": splat = "" } = useParams()
  const path = splat.replace(/\/+$/, "")
  const { user } = useAuth()
  const q = useFetch<Blob>(readPath("blob", owner, repo, ref, path))

  if (q.error instanceof ApiError && [400, 404].includes(q.error.status)) {
    return (
      <NotFoundBlock
        title="File not found"
        hint={`${q.error.message}. Check the address, or browse the repository to find the file.`}
      />
    )
  }
  if (q.error) return <ErrorBlock error={q.error} onRetry={q.reload} />
  if (!q.data) return <LoadingBlock label="Loading file" />

  const f = q.data
  const lines = f.content.split("\n")
  if (f.content.endsWith("\n")) lines.pop()

  return (
    <div className="flex flex-col gap-3">
      <PageToolbar
        up={{ to: uiPath("tree", owner, repo, ref, path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : ""), label: "folder" }}
      >
        <Breadcrumbs owner={owner} repo={repo} ref_={ref} path={path} lastIsFile />
      </PageToolbar>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="max-sm:min-w-0">
          <h1 className="text-xl font-semibold break-all">{f.name}</h1>
          <p className="text-xs text-muted-foreground">
            {formatSize(f.size)} · ref <span className="font-mono max-sm:break-all">{ref}</span>
          </p>
        </div>
        {canWrite(user, owner, ref) && (
          <Button asChild size="sm" variant="outline">
            <Link to={`/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/edit/${encodeURIComponent(ref)}/${path.split("/").map(encodeURIComponent).join("/")}`}>
              Edit
            </Link>
          </Button>
        )}
      </div>
      {f.size === 0 ? (
        <p className="rounded-md bg-card p-3 text-sm text-muted-foreground shadow-xs ring-1 ring-border">
          Empty file. This file has no content.
        </p>
      ) : (
        <div className="flex overflow-x-auto rounded-md bg-card font-mono text-sm shadow-xs ring-1 ring-border">
          <pre aria-hidden className="shrink-0 border-r border-border p-3 text-right text-muted-foreground select-none">
            {lines.map((_, i) => i + 1).join("\n")}
          </pre>
          <pre data-testid="file-content" className="p-3">{f.content}</pre>
        </div>
      )}
    </div>
  )
}
