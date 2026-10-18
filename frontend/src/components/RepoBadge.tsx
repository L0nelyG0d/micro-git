/** "Public" / "Private" pill shown next to a repository name, as on GitHub. */
export function RepoBadge({ isPrivate }: { isPrivate: boolean }) {
  return (
    <span className="inline-flex shrink-0 items-center rounded-full border border-border px-2 py-0.5 text-xs font-medium text-muted-foreground">
      {isPrivate ? "Private" : "Public"}
    </span>
  )
}
