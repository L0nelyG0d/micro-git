import type { ReactNode } from "react"
import { useId } from "react"

/** Titled block: a small muted title bar over a bordered card, so each part of a page is clearly outlined. */
export function Section({
  title,
  actions,
  className = "",
  children,
}: {
  title: string
  /** Controls shown at the right of the title bar. */
  actions?: ReactNode
  className?: string
  children: ReactNode
}) {
  const id = useId()
  return (
    <section
      aria-labelledby={id}
      className={`flex min-w-0 flex-col overflow-hidden rounded-md border border-border bg-card ${className}`}
    >
      <header className="flex items-center justify-between gap-2 border-b border-border bg-muted px-3 py-2">
        <h2 id={id} className="text-sm font-semibold">
          {title}
        </h2>
        {actions}
      </header>
      <div className="flex flex-col gap-2 p-3">{children}</div>
    </section>
  )
}
