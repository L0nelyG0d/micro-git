import { useState } from "react"
import type { FormEvent } from "react"
import { toast } from "sonner"
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
import { apiService, ApiError } from "@/services"
import { validateRepoName } from "@/lib/repos"
import { errorMessage } from "@/lib/useFetch"

const NAME_RULE = "Use 1-100 letters, digits, dots, hyphens or underscores."

export function RenameRepoDialog({
  owner,
  repo,
  onRenamed,
  onUnauthorized,
}: {
  owner: string
  repo: string
  onRenamed: (newName: string) => void
  onUnauthorized: () => void
}) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState(repo)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (busy) return
    const invalid = validateRepoName(name)
    setError(invalid)
    if (invalid) return
    setBusy(true)
    try {
      const updated = await apiService.updateRepo(owner, repo, { name })
      setOpen(false)
      toast.success(`Repository renamed to ${owner}/${updated.name}.`)
      onRenamed(updated.name)
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) onUnauthorized()
      if (err instanceof ApiError && err.status === 409) {
        setError("A repository with this name already exists. Choose another name.")
      } else if (err instanceof ApiError && err.status === 400) {
        setError(NAME_RULE)
      } else {
        setError(errorMessage(err))
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o)
        if (o) {
          setName(repo)
          setError("")
        }
      }}
    >
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          Rename
        </Button>
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit} noValidate className="flex flex-col gap-3">
          <DialogHeader>
            <DialogTitle>Rename repository</DialogTitle>
            <DialogDescription>
              Links to the old name stop working. The files and history stay the same.
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="rename-repo-name">Repository name</Label>
            <Input
              id="rename-repo-name"
              autoComplete="off"
              value={name}
              aria-invalid={!!error}
              aria-describedby={error ? "rename-repo-name-error" : undefined}
              onChange={(e) => setName(e.target.value)}
            />
            {error && (
              <p id="rename-repo-name-error" role="alert" className="text-sm text-destructive">
                {error}
              </p>
            )}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy}>
              Rename repository
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
