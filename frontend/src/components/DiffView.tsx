import type { FileDiff, Hunk } from "@/lib/git"

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

/** One file of a diff: header with path, status and counts, then the hunks. Shared by the commit and compare pages. */
export function FileView({ file }: { file: FileDiff }) {
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
    <section aria-label={file.path} className="overflow-hidden rounded-md border border-border bg-card">
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
