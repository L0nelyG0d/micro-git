import { useState } from "react"
import type { FormEvent } from "react"
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
import { isOidRef, type Branch } from "@/lib/git"
import { shortOid } from "@/lib/format"

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
}

export function BranchSwitcher(props: Props) {
  const { owner, repo, current, branches, canCreate, onSelect } = props
  const known = branches.some((b) => b.name === current)
  return (
    <div className="flex items-center gap-2">
      <Select value={current} onValueChange={onSelect}>
        <SelectTrigger aria-label="Branch" className="w-48">
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
    </div>
  )
}

function CreateBranch({ owner, repo, current, onCreated, onUnauthorized }: Props) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)

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
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoComplete="off"
              aria-invalid={!!error}
            />
            {error && (
              <p role="alert" className="text-xs text-destructive">
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
