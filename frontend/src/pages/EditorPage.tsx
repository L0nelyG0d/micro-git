import { useEffect, useState } from "react"
import type { FormEvent } from "react"
import { useNavigate, useParams } from "react-router-dom"
import { toast } from "sonner"
import { useAuth } from "@/auth/AuthContext"
import { BranchSwitcher } from "@/components/BranchSwitcher"
import { Breadcrumbs } from "@/components/Breadcrumbs"
import { ErrorBlock, LoadingBlock, NotFoundBlock } from "@/components/StateViews"
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { api, ApiError } from "@/lib/api"
import { canWrite, commitPath, readPath, uiPath, type Blob, type Branch, type Tree } from "@/lib/git"
import type { Repo } from "@/lib/repos"
import { errorMessage, useFetch } from "@/lib/useFetch"

type Change = { path: string; content: string } | { path: string; delete: true }

/**
 * `/:owner/:repo/edit/:ref/*` (existing file) and `/:owner/:repo/new/:ref`
 * (new file). One commit with a single change per submit.
 */
export function EditorPage({ mode }: { mode: "edit" | "new" }) {
  const { owner = "", repo: name = "", ref = "", "*": splat = "" } = useParams()
  const { user, loading: authLoading, expireSession } = useAuth()

  if (authLoading) return <LoadingBlock label="Loading editor" />
  if (!canWrite(user, owner, ref)) {
    return (
      <div role="alert" className="flex flex-col items-start gap-1" data-testid="forbidden">
        <h1 className="text-xl font-semibold">Forbidden</h1>
        <p className="text-sm text-muted-foreground">
          Only the repository owner can change files, and only on a branch. Log in as {owner} or
          open the file from a branch.
        </p>
      </div>
    )
  }
  return (
    <Editor
      mode={mode}
      owner={owner}
      name={name}
      ref_={ref}
      initialPath={splat.replace(/\/+$/, "")}
      onUnauthorized={expireSession}
    />
  )
}

function Editor({
  mode,
  owner,
  name,
  ref_,
  initialPath,
  onUnauthorized,
}: {
  mode: "edit" | "new"
  owner: string
  name: string
  ref_: string
  initialPath: string
  onUnauthorized: () => void
}) {
  const navigate = useNavigate()
  const base = `/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`
  const isNew = mode === "new"

  const [branch, setBranch] = useState(ref_)
  const [path, setPath] = useState(isNew ? "" : initialPath)
  const [text, setText] = useState<string | null>(isNew ? "" : null)
  const [message, setMessage] = useState("")
  const [touched, setTouched] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [conflict, setConflict] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)

  const repoQ = useFetch<Repo>(base)
  const empty = !!repoQ.data?.empty
  const branchesQ = useFetch<{ branches: Branch[] }>(repoQ.data && !empty ? `${base}/branches` : null)
  const effectiveBranch = empty ? (repoQ.data?.default_branch ?? ref_) : branch

  // The tip comes from the blob (edit) or the tree root (new) of the chosen branch.
  const tipPath = !repoQ.data || empty
    ? null
    : isNew
      ? readPath("tree", owner, name, branch, "")
      : readPath("blob", owner, name, branch, initialPath)
  const tipQ = useFetch<Blob | Tree>(tipPath)

  // Prefill once; later reloads only refresh the tip so typed text is never lost.
  useEffect(() => {
    if (!isNew && text === null && tipQ.data && "content" in tipQ.data) setText(tipQ.data.content)
  }, [isNew, text, tipQ.data])

  const reloadAll = () => {
    setConflict(false)
    setError("")
    repoQ.reload()
    branchesQ.reload()
    tipQ.reload()
  }

  const initialFailure = text === null && !isNew ? tipQ.error : undefined
  if (repoQ.error instanceof ApiError && repoQ.error.status === 404) {
    return (
      <NotFoundBlock
        title="Repository not found"
        hint={`There is no repository ${owner}/${name}. Check the address or go back to the list.`}
      />
    )
  }
  if (repoQ.error) return <ErrorBlock error={repoQ.error} onRetry={repoQ.reload} />
  if (initialFailure instanceof ApiError && [400, 404].includes(initialFailure.status)) {
    return (
      <NotFoundBlock
        title="File not found"
        hint={`${initialFailure.message}. Check the address, or browse the repository to find the file.`}
      />
    )
  }
  if (initialFailure) return <ErrorBlock error={initialFailure} onRetry={tipQ.reload} />
  if (isNew && tipQ.error instanceof ApiError && tipQ.error.status === 404 && !tipQ.data) {
    return (
      <NotFoundBlock
        title="Branch not found"
        hint={`${tipQ.error.message}. Pick another branch from the repository page.`}
      />
    )
  }
  if (!isNew && empty) {
    return (
      <NotFoundBlock
        title="File not found"
        hint="This repository is empty. Add a new file to make the first commit."
      />
    )
  }
  if (!repoQ.data || text === null) return <LoadingBlock label="Loading editor" />

  const tip = empty ? "" : tipQ.data?.commit
  const trimmed = message.trim()
  const messageError =
    trimmed.length === 0
      ? "Enter a commit message."
      : trimmed.length > 500
        ? "Commit message must be at most 500 characters."
        : ""
  const messageInvalid = !!messageError
  const pathMissing = isNew && path.trim() === ""
  const tipFailure = !empty && tipQ.error ? errorMessage(tipQ.error) : ""
  const ready = tip !== undefined && !tipQ.loading && !tipFailure && !repoQ.loading

  const send = async (change: Change) => {
    setBusy(true)
    setError("")
    setConflict(false)
    try {
      const res = await api<{ oid: string }>(`${base}/commits`, {
        method: "POST",
        body: { branch: effectiveBranch, expected_tip: tip, message: trimmed, changes: [change] },
      })
      toast.success("Changes committed.")
      navigate(commitPath(owner, name, res.oid))
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        onUnauthorized()
        navigate("/login")
        return
      }
      if (e instanceof ApiError && e.status === 409) setConflict(true)
      else setError(errorMessage(e))
    } finally {
      setBusy(false)
      setConfirmDelete(false)
    }
  }

  const submit = (ev: FormEvent) => {
    ev.preventDefault()
    setTouched(true)
    if (messageInvalid || pathMissing || !ready || busy) return
    void send({ path: isNew ? path.trim() : initialPath, content: text })
  }

  const askDelete = () => {
    setTouched(true)
    if (messageInvalid || !ready) return
    setConfirmDelete(true)
  }

  const branches: Branch[] = empty
    ? [{ name: effectiveBranch, oid: "", is_default: true }]
    : (branchesQ.data?.branches ?? [])
  const cancelTo = isNew
    ? uiPath("tree", owner, name, ref_, "")
    : uiPath("blob", owner, name, ref_, initialPath)

  return (
    <form onSubmit={submit} noValidate className="flex flex-col gap-3">
      {!isNew && <Breadcrumbs owner={owner} repo={name} ref_={ref_} path={initialPath} lastIsFile />}
      <h1 className="text-xl font-semibold break-all">
        {isNew ? "New file" : `Edit ${initialPath}`}
      </h1>

      {isNew && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="file-path">File path</Label>
          <Input
            id="file-path"
            value={path}
            onChange={(e) => setPath(e.target.value)}
            autoComplete="off"
            className="font-mono text-sm"
            aria-invalid={touched && pathMissing}
          />
          {touched && pathMissing ? (
            <p className="text-xs text-destructive">Enter a file path, for example src/main.go.</p>
          ) : (
            <p className="text-xs text-muted-foreground">Use / to create folders.</p>
          )}
        </div>
      )}

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="file-content">File content</Label>
        <Textarea
          id="file-content"
          value={text}
          onChange={(e) => setText(e.target.value)}
          spellCheck={false}
          className="min-h-64 font-mono text-sm"
        />
      </div>

      <div className="flex flex-col gap-3 rounded-xl bg-card p-3 shadow-xs ring-1 ring-border">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="commit-message">Commit message</Label>
          <Input
            id="commit-message"
            value={message}
            onChange={(e) => {
              setMessage(e.target.value)
              setTouched(true)
            }}
            onBlur={() => setTouched(true)}
            autoComplete="off"
            aria-invalid={touched && messageInvalid}
          />
          {touched && messageInvalid && (
            <p className="text-xs text-destructive">{messageError}</p>
          )}
        </div>

        <div className="flex flex-col gap-1.5">
          <span className="text-sm font-medium">Commit to branch</span>
          <BranchSwitcher
            owner={owner}
            repo={name}
            current={effectiveBranch}
            branches={branches}
            canCreate={false}
            onSelect={(b) => {
              setBranch(b)
              setConflict(false)
              setError("")
            }}
            onCreated={() => {}}
            onUnauthorized={onUnauthorized}
          />
        </div>

        {tipFailure && (
          <p role="alert" className="text-sm text-destructive">
            {tipFailure}
            {!isNew && " The file may not exist on this branch."}
          </p>
        )}
        {conflict && (
          <div role="alert" className="flex flex-col items-start gap-2 text-sm">
            <p className="text-destructive">
              Branch tip has moved. Someone committed to {effectiveBranch} since you opened this page.
            </p>
            <p className="text-muted-foreground">
              Reload gets the latest version and keeps your text. Review it, then commit again.
            </p>
            <Button type="button" variant="outline" size="sm" onClick={reloadAll}>
              Reload
            </Button>
          </div>
        )}
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}

        <div className="flex flex-wrap items-center gap-2">
          <Button type="submit" disabled={busy || !ready || messageInvalid || pathMissing}>
            Commit
          </Button>
          <Button type="button" variant="outline" onClick={() => navigate(cancelTo)}>
            Cancel
          </Button>
          {!isNew && (
            <Button
              type="button"
              variant="outline"
              className="ml-auto text-destructive"
              disabled={busy}
              onClick={askDelete}
            >
              Delete file
            </Button>
          )}
        </div>
      </div>

      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {initialPath}?</AlertDialogTitle>
            <AlertDialogDescription>
              The file will be removed from {effectiveBranch} in a new commit with your message.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <Button
              type="button"
              variant="destructive"
              disabled={busy}
              onClick={() => void send({ path: initialPath, delete: true })}
            >
              Delete file
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </form>
  )
}
