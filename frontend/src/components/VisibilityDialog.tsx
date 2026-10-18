import { useState } from "react"
import type { MouseEvent } from "react"
import { toast } from "sonner"
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { apiService, ApiError } from "@/services"
import { errorMessage } from "@/lib/useFetch"

/** Danger-zone control that flips a repository between public and private (PATCH `private`). */
export function VisibilityDialog({
  owner,
  repo,
  isPrivate,
  onChanged,
  onUnauthorized,
}: {
  owner: string
  repo: string
  isPrivate: boolean
  onChanged: () => void
  onUnauthorized: () => void
}) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const target = isPrivate ? "public" : "private"

  const confirm = async (ev: MouseEvent) => {
    ev.preventDefault() // stay open until the request finishes
    setBusy(true)
    setError("")
    try {
      await apiService.updateRepo(owner, repo, { private: !isPrivate })
      setOpen(false)
      toast.success(`${owner}/${repo} is now ${target}.`)
      onChanged()
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) onUnauthorized()
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <AlertDialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o)
        if (!o) setError("")
      }}
    >
      <AlertDialogTrigger asChild>
        <Button variant="outline" size="sm" className="text-destructive">
          Change visibility
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Make {owner}/{repo} {target}?</AlertDialogTitle>
          <AlertDialogDescription>
            {isPrivate
              ? "Anyone will be able to see this repository, its files and its history."
              : "Only you will be able to see this repository. Everyone else will get a “not found” page."}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <Button variant="destructive" disabled={busy} aria-busy={busy || undefined} onClick={confirm}>
            {busy ? "Saving…" : `Make ${target}`}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
