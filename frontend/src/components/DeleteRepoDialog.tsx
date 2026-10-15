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
import { api, ApiError } from "@/lib/api"
import { errorMessage } from "@/lib/useFetch"

export function DeleteRepoDialog({
  owner,
  repo,
  onDeleted,
  onUnauthorized,
}: {
  owner: string
  repo: string
  onDeleted: () => void
  onUnauthorized: () => void
}) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")

  const confirm = async (ev: MouseEvent) => {
    ev.preventDefault() // stay open until the request finishes
    setBusy(true)
    setError("")
    try {
      await api(`/repos/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}`, {
        method: "DELETE",
      })
      setOpen(false)
      toast.success(`Repository ${owner}/${repo} deleted.`)
      onDeleted()
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
          Delete repository
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {owner}/{repo}?</AlertDialogTitle>
          <AlertDialogDescription>
            The repository {owner}/{repo} and all its commits will be deleted. This cannot be undone.
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
            Delete repository
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
