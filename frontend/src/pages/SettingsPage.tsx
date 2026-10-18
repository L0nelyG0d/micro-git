import { useState } from "react"
import type { FormEvent } from "react"
import { Link, useNavigate, useParams } from "react-router-dom"
import { toast } from "sonner"
import { isOwner, useAuth } from "@/auth/AuthContext"
import { BranchSwitcher } from "@/components/BranchSwitcher"
import { DeleteRepoDialog } from "@/components/DeleteRepoDialog"
import { RepoHeader } from "@/components/RepoHeader"
import { VisibilityDialog } from "@/components/VisibilityDialog"
import { RenameRepoDialog } from "@/components/RenameRepoDialog"
import { ErrorBlock, LoadingBlock, NotFoundBlock } from "@/components/StateViews"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { api, ApiError } from "@/lib/api"
import { formatDate } from "@/lib/format"
import { historyPath, uiPath, type Branch } from "@/lib/git"
import type { Repo } from "@/lib/repos"
import { errorMessage, useFetch } from "@/lib/useFetch"

const MAX_DESCRIPTION = 500

/** Description and default branch, saved together with PATCH /repos/{owner}/{repo}. */
function DetailsForm({
  repo,
  branches,
  onSaved,
  onUnauthorized,
}: {
  repo: Repo
  branches: Branch[]
  onSaved: () => void
  onUnauthorized: () => void
}) {
  const [description, setDescription] = useState(repo.description)
  const [defaultBranch, setDefaultBranch] = useState(repo.default_branch)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const tooLong = description.length > MAX_DESCRIPTION
  const changed = description !== repo.description || defaultBranch !== repo.default_branch

  const submit = async (ev: FormEvent) => {
    ev.preventDefault()
    if (!changed || tooLong || busy) return
    setBusy(true)
    setError("")
    try {
      await api(`/repos/${encodeURIComponent(repo.owner)}/${encodeURIComponent(repo.name)}`, {
        method: "PATCH",
        body: {
          ...(description !== repo.description && { description }),
          ...(defaultBranch !== repo.default_branch && { default_branch: defaultBranch }),
        },
      })
      toast.success("Settings saved.")
      onSaved()
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) onUnauthorized()
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate className="flex flex-col gap-3">
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="repo-description">Description</Label>
        <Textarea
          id="repo-description"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          aria-invalid={tooLong}
          className="min-h-20"
        />
        <p className={`text-xs ${tooLong ? "text-destructive" : "text-muted-foreground"}`}>
          {description.length}/{MAX_DESCRIPTION} characters
        </p>
      </div>
      {!repo.empty && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="default-branch">Default branch</Label>
          <Select value={defaultBranch} onValueChange={setDefaultBranch} disabled={branches.length === 0}>
            <SelectTrigger id="default-branch" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {branches.map((b) => (
                <SelectItem key={b.name} value={b.name}>
                  {b.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      <div>
        <Button type="submit" size="sm" disabled={busy || !changed || tooLong}>
          Save changes
        </Button>
      </div>
    </form>
  )
}

/** `/:owner/:repo/settings`: owner-only management of the repository, its branches and history. */
export function SettingsPage() {
  const { owner = "", repo: name = "" } = useParams()
  const navigate = useNavigate()
  const { user, loading: sessionLoading, expireSession } = useAuth()
  const base = `/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`
  const repoQ = useFetch<Repo>(base)
  const branchesQ = useFetch<{ branches: Branch[] }>(`${base}/branches`) // parallel with the repo request
  const [historyRef, setHistoryRef] = useState("")

  if (repoQ.error instanceof ApiError && repoQ.error.status === 404) {
    return (
      <NotFoundBlock
        title="Repository not found"
        hint={`There is no repository ${owner}/${name}. Check the address or go back to the list.`}
      />
    )
  }
  if (repoQ.error) return <ErrorBlock error={repoQ.error} onRetry={repoQ.reload} />
  if (!repoQ.data || sessionLoading) return <LoadingBlock label="Loading settings" />

  const repo = repoQ.data
  const repoHome = `/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`
  const toolbar = <RepoHeader repo={repo} active="settings" owns={isOwner(user, owner)} />

  if (!isOwner(user, owner)) {
    return (
      <div className="flex flex-col gap-3">
        {toolbar}
        <h1 className="text-xl font-semibold">Settings</h1>
        <p className="rounded-md bg-card p-3 text-sm text-muted-foreground border border-border">
          Only the owner of {owner}/{name} can change its settings.{" "}
          {!user && (
            <Link to="/login" state={{ from: `${repoHome}/settings` }} className="text-primary underline underline-offset-2">
              Log in
            </Link>
          )}
        </p>
      </div>
    )
  }

  const branches = branchesQ.data?.branches ?? []
  const chosen = historyRef || repo.default_branch
  const nav = [
    { id: "general", label: "General" },
    { id: "history", label: "Commit history" },
    { id: "branches", label: "Branches" },
    { id: "danger-zone", label: "Danger zone" },
  ].filter((n) => !(repo.empty && n.id === "branches"))

  return (
    <div className="flex flex-col gap-6">
      {toolbar}
      <div className="grid gap-6 md:grid-cols-[14rem_minmax(0,1fr)]">
        <nav aria-label="Settings sections" className="flex flex-row flex-wrap gap-1 md:flex-col">
          {nav.map((n) => (
            <a
              key={n.id}
              href={`#${n.id}`}
              className="rounded-md px-3 py-1.5 text-sm whitespace-nowrap hover:bg-accent"
            >
              {n.label}
            </a>
          ))}
        </nav>

        <div className="flex min-w-0 flex-col gap-6">
          <section id="general" aria-labelledby="general-h" className="flex flex-col gap-3 scroll-mt-20">
            <h2 id="general-h" className="border-b border-border pb-2 text-2xl font-normal">
              General
            </h2>
            <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
              <dt className="text-muted-foreground">Name</dt>
              <dd className="font-medium break-all">{repo.name}</dd>
              <dt className="text-muted-foreground">Visibility</dt>
              <dd>{repo.private ? "Private" : "Public"}</dd>
              <dt className="text-muted-foreground">Created</dt>
              <dd>{formatDate(repo.created_at)}</dd>
            </dl>
            <DetailsForm
              key={`${repo.description}\n${repo.default_branch}`}
              repo={repo}
              branches={branches}
              onSaved={repoQ.reload}
              onUnauthorized={expireSession}
            />
            <div>
              <RenameRepoDialog
                owner={owner}
                repo={name}
                onUnauthorized={expireSession}
                onRenamed={(n) => navigate(`/${encodeURIComponent(owner)}/${encodeURIComponent(n)}/settings`, { replace: true })}
              />
            </div>
          </section>

          <section id="history" aria-labelledby="history-h" className="flex flex-col gap-3 scroll-mt-20">
            <h2 id="history-h" className="border-b border-border pb-2 text-2xl font-normal">
              Commit history
            </h2>
            {repo.empty ? (
              <p className="text-sm text-muted-foreground">Nothing has been committed yet. Add a file to start the history.</p>
            ) : (
              <>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="history-branch">Branch</Label>
                  <Select value={chosen} onValueChange={setHistoryRef} disabled={branches.length === 0}>
                    <SelectTrigger id="history-branch" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {branches.map((b) => (
                        <SelectItem key={b.name} value={b.name}>
                          {b.name}
                          {b.is_default ? " (default)" : ""}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="flex flex-wrap gap-2">
                  <Button asChild size="sm">
                    <Link to={historyPath(owner, name, chosen)}>View history</Link>
                  </Button>
                  <Button asChild size="sm" variant="outline">
                    <Link to={`${repoHome}/compare`}>Compare branches</Link>
                  </Button>
                </div>
              </>
            )}
          </section>

          {!repo.empty && (
            <section id="branches" aria-labelledby="branches-h" className="flex flex-col gap-3 scroll-mt-20">
              <h2 id="branches-h" className="border-b border-border pb-2 text-2xl font-normal">
                Branches
              </h2>
              {branchesQ.error ? (
                <ErrorBlock error={branchesQ.error} onRetry={branchesQ.reload} />
              ) : !branchesQ.data ? (
                <LoadingBlock label="Loading branches" />
              ) : (
                <>
                  <ul className="divide-y divide-border rounded-md border border-border text-sm">
                    {branches.map((b) => (
                      <li key={b.name} className="flex items-center justify-between gap-2 px-3 py-2">
                        <Link to={uiPath("tree", owner, name, b.name, "")} className="font-mono text-primary break-all hover:underline">
                          {b.name}
                        </Link>
                        <span className="text-xs text-muted-foreground">{b.is_default ? "default" : ""}</span>
                      </li>
                    ))}
                  </ul>
                  <BranchSwitcher
                    owner={owner}
                    repo={name}
                    current={repo.default_branch}
                    branches={branches}
                    canCreate
                    onSelect={(b) => navigate(uiPath("tree", owner, name, b, ""))}
                    onCreated={() => branchesQ.reload()}
                    onUnauthorized={expireSession}
                    canDelete
                    onDeleted={() => branchesQ.reload()}
                  />
                </>
              )}
            </section>
          )}

          <section id="danger-zone" aria-labelledby="danger-h" className="flex flex-col gap-3 scroll-mt-20">
            <h2 id="danger-h" className="border-b border-border pb-2 text-2xl font-normal">
              Danger zone
            </h2>
            <ul className="divide-y divide-destructive/40 rounded-md border border-destructive/60">
              <li className="flex flex-wrap items-center justify-between gap-3 p-4">
                <div className="min-w-0">
                  <p className="text-sm font-semibold">Change repository visibility</p>
                  <p className="text-sm text-muted-foreground">
                    This repository is currently {repo.private ? "private" : "public"}.
                  </p>
                </div>
                <VisibilityDialog
                  owner={owner}
                  repo={name}
                  isPrivate={repo.private}
                  onChanged={repoQ.reload}
                  onUnauthorized={expireSession}
                />
              </li>
              <li className="flex flex-wrap items-center justify-between gap-3 p-4">
                <div className="min-w-0">
                  <p className="text-sm font-semibold">Delete this repository</p>
                  <p className="text-sm text-muted-foreground">
                    Deleting a repository removes all its branches and commits. This cannot be undone.
                  </p>
                </div>
                <DeleteRepoDialog owner={owner} repo={name} onUnauthorized={expireSession} onDeleted={() => navigate("/")} />
              </li>
            </ul>
          </section>
        </div>
      </div>
    </div>
  )
}
