import { Link, useParams } from "react-router-dom"
import { ErrorBlock, LoadingBlock, NotFoundBlock } from "@/components/StateViews"
import { OidLink } from "@/components/OidLink"
import { ApiError } from "@/lib/api"
import { formatDate } from "@/lib/format"
import { commitPath, uiPath, type CommitDetail, type FileDiff, type Hunk } from "@/lib/git"
import { useFetch } from "@/lib/useFetch"

const MARK = { context: " ", add: "+", del: "-" } as const
const ROW = {
  context: "",
  add: "bg-success/10 text-success",
  del: "bg-destructive/10 text-destructive",
} as const

function range(start: number, lines: number, sign: "-" | "+") {
  return `${sign}${start},${lines}`
}

function HunkView({ hunk, status }: { hunk: Hunk; status: FileDiff["status"] }) {
  let o = hunk.old_start
  let n = hunk.new_start
  const parts = [
    status !== "added" ? range(hunk.old_start, hunk.old_lines, "-") : "",
    status !== "deleted" ? range(hunk.new_start, hunk.new_lines, "+") : "",
  ].filter(Boolean)
  return (
    <div>
      <div className="bg-muted/50 px-3 py-1 text-xs text-muted-foreground" data-testid="hunk-header">
        @@ {parts.join(" ")} @@
      </div>
      {hunk.lines.map((l, i) => {
        const oldNo = l.type !== "add" ? o++ : ""
        const newNo = l.type !== "del" ? n++ : ""
        return (
          <div key={i} className={`flex ${ROW[l.type]}`} data-testid={`line-${l.type}`}>
            <span aria-hidden className="w-10 shrink-0 px-1 text-right select-none opacity-70">
              {oldNo}
            </span>
            <span aria-hidden className="w-10 shrink-0 px-1 text-right select-none opacity-70">
              {newNo}
            </span>
            <span className="w-5 shrink-0 text-center font-semibold select-none">
              <span className="sr-only">{l.type === "add" ? "added " : l.type === "del" ? "removed " : ""}</span>
              {MARK[l.type]}
            </span>
            <span className="pr-3 whitespace-pre">{l.text}</span>
          </div>
        )
      })}
    </div>
  )
}

function FileView({ file }: { file: FileDiff }) {
  let content
  if (file.truncated) {
    content = <p className="p-3 text-sm text-muted-foreground">diff too large to display</p>
  } else if (file.status === "modified" && file.hunks.length === 0) {
    content = (
      <p className="p-3 text-sm text-muted-foreground">
        no visible changes (only the final newline differs)
      </p>
    )
  } else {
    content = (
      <div className="overflow-x-auto font-mono text-sm">
        <div className="min-w-max">
          {file.hunks.map((h, i) => (
            <HunkView key={i} hunk={h} status={file.status} />
          ))}
        </div>
      </div>
    )
  }
  return (
    <section aria-label={file.path} className="overflow-hidden rounded-xl bg-card shadow-xs ring-1 ring-border">
      <header className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-1.5 text-sm">
        <span className="font-mono break-all">{file.path}</span>
        <span className="rounded-md px-1.5 text-xs ring-1 ring-border" data-testid="file-status">
          {file.status}
        </span>
        <span className="ml-auto text-xs text-muted-foreground">
          +{file.additions} -{file.deletions}
        </span>
      </header>
      {content}
    </section>
  )
}

/** `/:owner/:repo/commit/:oid` */
export function CommitPage() {
  const { owner = "", repo = "", oid = "" } = useParams()
  const q = useFetch<CommitDetail>(
    `/repos/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/commits/${encodeURIComponent(oid)}`,
  )

  if (q.error instanceof ApiError && [400, 404].includes(q.error.status)) {
    return (
      <NotFoundBlock
        title="Commit not found"
        hint={`${q.error.message}. Check the address, or browse the history to find the commit.`}
      />
    )
  }
  if (q.error) return <ErrorBlock error={q.error} onRetry={q.reload} />
  if (!q.data) return <LoadingBlock label="Loading commit" />

  const c = q.data
  const adds = c.files.reduce((s, f) => s + f.additions, 0)
  const dels = c.files.reduce((s, f) => s + f.deletions, 0)
  const count = c.files.length

  return (
    <div className="flex flex-col gap-3">
      <Link to={uiPath("tree", owner, repo, c.oid, "")} className="text-sm text-primary hover:underline">
        {owner}/{repo}
      </Link>
      <div className="flex flex-col gap-1 rounded-xl bg-card p-3 shadow-xs ring-1 ring-border">
        <h1 className="text-xl font-semibold whitespace-pre-wrap break-words">{c.message}</h1>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
          <span>{c.author}</span>
          <span>{formatDate(c.time)}</span>
          <span className="inline-flex items-center gap-1">
            commit <OidLink oid={c.oid} to={commitPath(owner, repo, c.oid)} copyable />
          </span>
          <span className="inline-flex items-center gap-1">
            {c.parents.length === 0 ? (
              "root commit"
            ) : (
              <>
                {c.parents.length > 1 ? "parents" : "parent"}
                {c.parents.map((p) => (
                  <OidLink key={p} oid={p} to={commitPath(owner, repo, p)} />
                ))}
              </>
            )}
          </span>
        </div>
      </div>
      <p className="text-sm font-semibold" data-testid="summary">
        {count} {count === 1 ? "file" : "files"} changed, {adds} {adds === 1 ? "addition" : "additions"},{" "}
        {dels} {dels === 1 ? "deletion" : "deletions"}
      </p>
      {count === 0 ? (
        <p className="rounded-xl bg-card p-3 text-sm text-muted-foreground shadow-xs ring-1 ring-border">
          no files changed
        </p>
      ) : (
        c.files.map((f) => <FileView key={f.path} file={f} />)
      )}
    </div>
  )
}
