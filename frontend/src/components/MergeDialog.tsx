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
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { api, ApiError } from "@/lib/api"
import type { Branch } from "@/lib/git"
import { errorMessage } from "@/lib/useFetch"

type Props = {
  owner: string
  repo: string
  branches: Branch[]
  defaultBranch: string
  /** Branch currently shown; preferred as the "Merge from" default. */
  current: string
  onUnauthorized: () => void
  /** Called after a successful merge, with the target branch. */
  onMerged: (into: string) => void
  /** Called by the "Reload" action after `branch tip has moved`. */
  onReload: () => void
}

function BranchSelect({
  id,
  label,
  value,
  branches,
  onChange,
}: {
  id: string
  label: string
  value: string
  branches: Branch[]
  onChange: (v: string) => void
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger id={id} className="w-full">
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
  )
}

/** Owner-only fast-forward merge (POST /merge). */
export function MergeDialog({
  owner,
  repo,
  branches,
  defaultBranch,
  current,
  onUnauthorized,
  onMerged,
  onReload,
}: Props) {
  const [open, setOpen] = useState(false)
  const [from, setFrom] = useState("")
  const [into, setInto] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ApiError | string>("")

  const reset = (o: boolean) => {
    setOpen(o)
    setError("")
    if (o) {
      const target = branches.some((b) => b.name === defaultBranch) ? defaultBranch : branches[0].name
      const others = branches.filter((b) => b.name !== target)
      setInto(target)
      setFrom(others.find((b) => b.name === current)?.name ?? others[0]?.name ?? "")
    }
  }

  const submit = async (ev: MouseEvent) => {
    ev.preventDefault() // stay open until the request finishes
    setBusy(true)
    setError("")
    try {
      await api(`/repos/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/merge`, {
        method: "POST",
        body: { base: into, head: from },
      })
      setOpen(false)
      toast.success(`${into} now points to the tip of ${from}.`)
      onMerged(into)
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) onUnauthorized()
      setError(e instanceof ApiError ? e : errorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  const message = typeof error === "string" ? error : error ? error.message : ""
  const notFF = error instanceof ApiError && message === "not a fast-forward"
  const moved = error instanceof ApiError && message === "branch tip has moved"

  return (
    <AlertDialog open={open} onOpenChange={reset}>
      <AlertDialogTrigger asChild>
        <Button variant="outline" size="sm" className="mr-auto">
          Merge
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Merge branches</AlertDialogTitle>
          <AlertDialogDescription>
            Fast-forward only. {into} will point to the tip of {from}.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div className="flex flex-col gap-3">
          <BranchSelect id="merge-from" label="Merge from" value={from} branches={branches} onChange={setFrom} />
          <BranchSelect id="merge-into" label="Into" value={into} branches={branches} onChange={setInto} />
        </div>
        {error && (
          <div role="alert" className="flex flex-col items-start gap-2 text-sm text-destructive">
            <p>
              {message}
              {notFF && ". Create a new branch or commit on top of the target branch instead."}
            </p>
            {moved && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  setOpen(false)
                  setError("")
                  onReload()
                }}
              >
                Reload
              </Button>
            )}
          </div>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <Button disabled={busy || from === into || !from} onClick={submit}>
            Merge
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
