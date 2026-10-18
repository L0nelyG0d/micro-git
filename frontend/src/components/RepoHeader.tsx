import { BookMarked, GitCommitHorizontal, GitCompareArrows, Settings } from "lucide-react"
import { Link } from "react-router-dom"
import { RepoBadge } from "@/components/RepoBadge"
import type { Repo } from "@/lib/repos"

type Tab = "code" | "commits" | "compare" | "settings"

/** GitHub-style repository header: owner / name with a visibility badge, then the tab row. */
export function RepoHeader({ repo, active, owns }: { repo: Repo; active: Tab; owns: boolean }) {
  const { owner, name } = repo
  const home = `/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`
  const tabs: { id: Tab; label: string; to: string; icon: typeof Settings; show: boolean }[] = [
    { id: "code", label: "Code", to: home, icon: BookMarked, show: true },
    { id: "commits", label: "Commits", to: `${home}/commits/${encodeURIComponent(repo.default_branch)}`, icon: GitCommitHorizontal, show: !repo.empty },
    { id: "compare", label: "Compare", to: `${home}/compare`, icon: GitCompareArrows, show: !repo.empty },
    { id: "settings", label: "Settings", to: `${home}/settings`, icon: Settings, show: owns },
  ]
  return (
    <header className="-mx-4 border-b border-border bg-muted px-4 pt-4 md:-mt-6">
      <div className="mx-auto flex max-w-6xl flex-col gap-3">
        <div className="flex flex-wrap items-center gap-2 text-xl">
          <h1 className="flex max-w-full min-w-0 flex-wrap items-center break-all">
            <Link to={`/${encodeURIComponent(owner)}`} className="text-primary hover:underline">
              {owner}
            </Link>
            <span className="px-1 text-muted-foreground">/</span>
            <Link to={home} className="font-semibold text-primary hover:underline">
              {name}
            </Link>
          </h1>
          <RepoBadge isPrivate={repo.private} />
        </div>
        {repo.description && <p className="text-sm break-words text-muted-foreground">{repo.description}</p>}
        <nav aria-label="Repository" className="-mb-px flex flex-wrap gap-x-0.5 sm:gap-x-1">
          {tabs
            .filter((t) => t.show)
            .map((t) => (
              <Link
                key={t.id}
                to={t.to}
                aria-current={t.id === active ? "page" : undefined}
                className={`flex items-center gap-1.5 border-b-2 px-2 py-2 text-sm sm:gap-2 sm:px-3 whitespace-nowrap hover:bg-accent ${
                  t.id === active ? "border-primary font-semibold" : "border-transparent text-muted-foreground"
                }`}
              >
                <t.icon className="hidden size-4 sm:block" aria-hidden />
                {t.label}
              </Link>
            ))}
        </nav>
      </div>
    </header>
  )
}
