/** Round initial-letter avatar; there are no uploaded pictures. */
export function Avatar({ name, className = "size-8 text-sm" }: { name: string; className?: string }) {
  return (
    <span
      aria-hidden
      className={`inline-flex shrink-0 items-center justify-center rounded-full bg-accent font-semibold text-accent-foreground uppercase ${className}`}
    >
      {name.slice(0, 1)}
    </span>
  )
}
