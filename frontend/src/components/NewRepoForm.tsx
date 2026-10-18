import { useEffect, useRef, useState } from "react"
import type { FormEvent } from "react"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { api, ApiError } from "@/lib/api"
import { validateDescription, validateRepoName } from "@/lib/repos"
import type { Repo } from "@/lib/repos"

type Props = {
  onCreated: (repo: Repo) => void
  onCancel: () => void
  /** Called when the server answers 401. */
  onUnauthorized: () => void
}

export function NewRepoForm({ onCreated, onCancel, onUnauthorized }: Props) {
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")
  const [isPrivate, setIsPrivate] = useState(false)
  const [nameError, setNameError] = useState("")
  const [descError, setDescError] = useState("")
  const [formError, setFormError] = useState("")
  const [busy, setBusy] = useState(false)
  const nameRef = useRef<HTMLInputElement>(null)
  const descRef = useRef<HTMLTextAreaElement>(null)

  // The trigger button unmounts when the form opens, so move focus here.
  useEffect(() => {
    nameRef.current?.focus()
  }, [])

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (busy) return
    const ne = validateRepoName(name)
    const de = validateDescription(description)
    setNameError(ne)
    setDescError(de)
    setFormError("")
    if (ne || de) {
      ;(ne ? nameRef : descRef).current?.focus()
      return
    }
    setBusy(true)
    try {
      const repo = await api<Repo>("/repos", { method: "POST", body: { name, description, private: isPrivate } })
      onCreated(repo)
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setFormError("Your session has expired. Log in again to create a repository.")
        onUnauthorized()
      } else if (err instanceof ApiError && err.status === 409) {
        setNameError(err.message)
        nameRef.current?.focus()
      } else if (err instanceof ApiError && err.status === 400) {
        if (/description/i.test(err.message)) {
          setDescError(err.message)
          descRef.current?.focus()
        } else {
          setNameError(err.message)
          nameRef.current?.focus()
        }
      } else {
        setFormError(
          err instanceof ApiError
            ? err.message
            : "Could not reach the server. Check your connection and try again.",
        )
      }
      setBusy(false)
    }
  }

  return (
    <Card className="p-3">
      <form onSubmit={submit} noValidate className="flex flex-col gap-3" aria-label="New repository">
        <h2 className="font-semibold">New repository</h2>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="repo-name">Name</Label>
          <Input
            id="repo-name"
            name="name"
            ref={nameRef}
            autoComplete="off"
            spellCheck={false}
            required
            value={name}
            aria-invalid={!!nameError}
            aria-describedby={nameError ? "repo-name-error" : undefined}
            onChange={(e) => setName(e.target.value)}
          />
          {nameError && (
            <p id="repo-name-error" role="alert" className="text-sm text-destructive">
              {nameError}
            </p>
          )}
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="repo-description">Description (optional)</Label>
          <Textarea
            id="repo-description"
            name="description"
            ref={descRef}
            value={description}
            aria-invalid={!!descError}
            aria-describedby={descError ? "repo-description-error" : undefined}
            onChange={(e) => setDescription(e.target.value)}
          />
          {descError && (
            <p id="repo-description-error" role="alert" className="text-sm text-destructive">
              {descError}
            </p>
          )}
        </div>
        <fieldset className="flex flex-col gap-2">
          <legend className="mb-1 text-sm font-medium">Visibility</legend>
          <label className="flex items-start gap-2 text-sm">
            <input type="radio" name="visibility" checked={!isPrivate} onChange={() => setIsPrivate(false)} className="mt-1" />
            <span>
              <span className="font-medium">Public</span>
              <span className="block text-xs text-muted-foreground">Anyone can see this repository.</span>
            </span>
          </label>
          <label className="flex items-start gap-2 text-sm">
            <input type="radio" name="visibility" checked={isPrivate} onChange={() => setIsPrivate(true)} className="mt-1" />
            <span>
              <span className="font-medium">Private</span>
              <span className="block text-xs text-muted-foreground">Only you can see this repository.</span>
            </span>
          </label>
        </fieldset>
        {formError && (
          <p role="alert" className="text-sm text-destructive">
            {formError}
          </p>
        )}
        <div className="flex gap-2">
          <Button type="submit" disabled={busy}>
            Create repository
          </Button>
          <Button type="button" variant="outline" onClick={onCancel}>
            Cancel
          </Button>
        </div>
      </form>
    </Card>
  )
}
