import { useRef, useState } from "react"
import type { FormEvent, MouseEvent } from "react"
import { toast } from "sonner"
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { api, ApiError } from "@/lib/api"
import { isOidRef, validateBranchName, type Branch } from "@/lib/git"
import { shortOid } from "@/lib/format"
import { errorMessage } from "@/lib/useFetch"

type Props = {
  owner: string
  repo: string
  /** Currently selected ref: a branch name or a commit oid. */
  current: string
  branches: Branch[]
  /** Owner on a branch: shows "Create branch". */
  canCreate: boolean
  onSelect: (ref: string) => void
  /** Called after POST succeeded, with the new branch name. */
  onCreated: (name: string) => void
  onUnauthorized: () => void
  /** Owner only: shows "Delete branch". */
  canDelete?: boolean
  /** Called after DELETE succeeded, with the deleted branch name. */
  onDeleted?: (name: string) => void
}

export function BranchSwitcher(props: Props) {
  const { owner, repo, current, branches, canCreate, onSelect } = props
  const known = branches.some((b) => b.name === current)
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Select value={current} onValueChange={onSelect}>
        <SelectTrigger aria-label="Branch" className="w-full sm:w-48">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {!known && (
            <SelectItem value={current}>{isOidRef(current) ? shortOid(current) : current}</SelectItem>
          )}
          {branches.map((b) => (
            <SelectItem key={b.name} value={b.name}>
              {b.name}
              {b.is_default ? " (default)" : ""}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {canCreate && <CreateBranch {...props} owner={owner} repo={repo} />}
      {props.canDelete && <DeleteBranch {...props} />}
    </div>
  )
}

function CreateBranch({ owner, repo, current, onCreated, onUnauthorized }: Props) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  const reset = (o: boolean) => {
    setOpen(o)
    if (!o) {
      setName("")
      setError("")
    }
  }

  const submit = async (ev: FormEvent) => {
    ev.preventDefault()
    const trimmed = name.trim()
    if (!trimmed) {
      setError("Enter a branch name.")
      inputRef.current?.focus()
      return
    }
    const invalid = validateBranchName(trimmed)
    if (invalid) {
      setError(invalid)
      inputRef.current?.focus()
      return
    }
    setBusy(true)
    setError("")
    try {
      await api(`/repos/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/branches`, {
        method: "POST",
        body: { name: trimmed, from: current },
      })
      reset(false)
      onCreated(trimmed)
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) onUnauthorized()
      setError(
        e instanceof ApiError
          ? e.message
          : "Could not reach the server. Check your connection and try again.",
      )
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={reset}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          Create branch
        </Button>
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-3" noValidate>
          <DialogHeader>
            <DialogTitle>Create branch</DialogTitle>
            <DialogDescription>
              The new branch starts from {current}.
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-branch-name">Branch name</Label>
            <Input
              id="new-branch-name"
              name="branch"
              ref={inputRef}
              spellCheck={false}
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoComplete="off"
              aria-invalid={!!error}
              aria-describedby={error ? "new-branch-name-error" : undefined}
            />
            {error && (
              <p id="new-branch-name-error" role="alert" className="text-xs text-destructive">
                {error}
              </p>
            )}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => reset(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy}>
              Create branch
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function DeleteBranch({ owner, repo, branches, onDeleted, onUnauthorized }: Props) {
  const [listOpen, setListOpen] = useState(false)
  const [target, setTarget] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const deletable = branches.filter((b) => !b.is_default)
  if (deletable.length === 0) return null

  const confirm = async (ev: MouseEvent) => {
    ev.preventDefault() // stay open until the request finishes
    if (!target) return
    setBusy(true)
    setError("")
    try {
      await api(
        `/repos/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/branches/${encodeURIComponent(target)}`,
        { method: "DELETE" },
      )
      const name = target
      setTarget(null)
      toast.success(`Branch ${name} deleted.`)
      onDeleted?.(name)
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) onUnauthorized()
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Button variant="outline" size="sm" className="text-destructive" onClick={() => setListOpen(true)}>
        Delete branch
      </Button>
      <Dialog open={listOpen} onOpenChange={setListOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete branch</DialogTitle>
            <DialogDescription>Choose a branch to delete. The default branch cannot be deleted.</DialogDescription>
          </DialogHeader>
          <ul className="flex flex-col gap-1.5">
            {deletable.map((b) => (
              <li key={b.name} className="flex items-center justify-between gap-2 text-sm">
                <span className="font-mono break-all">{b.name}</span>
                <Button
                  variant="outline"
                  size="sm"
                  className="text-destructive"
                  aria-label={`Delete ${b.name}`}
                  onClick={() => {
                    setListOpen(false)
                    setError("")
                    setTarget(b.name)
                  }}
                >
                  Delete
                </Button>
              </li>
            ))}
          </ul>
        </DialogContent>
      </Dialog>
      <AlertDialog open={target !== null} onOpenChange={(o) => !o && setTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete branch {target}?</AlertDialogTitle>
            <AlertDialogDescription>
              The branch {target} will be deleted. Its commits stay available from other branches that contain them.
            </AlertDialogDescription>
          </AlertDialogHeader>
          {error && (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <Button variant="destructive" disabled={busy} onClick={confirm}>
              Delete branch
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
