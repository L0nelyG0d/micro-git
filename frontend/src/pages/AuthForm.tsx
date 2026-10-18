import { useRef, useState } from "react"
import type { FormEvent, ReactNode } from "react"
import { Link } from "react-router-dom"
import { ApiError } from "@/services"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

type Props = {
  title: string
  submitLabel: string
  notice?: string
  footer: ReactNode
  /** Resolves on success; throws an Error whose message is shown inline. */
  onSubmit: (username: string, password: string) => Promise<void>
}

function formatWait(seconds?: number): string {
  if (!seconds) return "a little"
  if (seconds < 60) return `${seconds} second${seconds === 1 ? "" : "s"}`
  const m = Math.ceil(seconds / 60)
  return `${m} minute${m === 1 ? "" : "s"}`
}

export function AuthForm({ title, submitLabel, notice, footer, onSubmit }: Props) {
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [invalid, setInvalid] = useState<"" | "username" | "password">("")
  const usernameRef = useRef<HTMLInputElement>(null)
  const passwordRef = useRef<HTMLInputElement>(null)
  const errorId = "auth-error"

  async function handle(e: FormEvent) {
    e.preventDefault()
    if (busy) return
    if (!username.trim() || !password) {
      const missingUser = !username.trim()
      setError(missingUser ? "Enter your username." : "Enter your password.")
      setInvalid(missingUser ? "username" : "password")
      ;(missingUser ? usernameRef : passwordRef).current?.focus()
      return
    }
    setBusy(true)
    setError("")
    setInvalid("")
    try {
      await onSubmit(username, password)
    } catch (err) {
      setError(
        err instanceof ApiError && err.status === 429
          ? `Too many attempts. Please wait ${formatWait(err.retryAfter)} and try again.`
          : err instanceof Error
            ? err.message
            : "Something went wrong. Try again.",
      )
      setPassword("") // the password is always cleared after a failure
      // A taken username (409 on register) is the username's fault; anything else points at the password.
      const field = err instanceof ApiError && err.status === 409 ? "username" : "password"
      setInvalid(field)
      setBusy(false)
      ;(field === "username" ? usernameRef : passwordRef).current?.focus()
    }
  }

  return (
    <Card className="mx-auto mt-6 w-full max-w-sm bg-muted p-4">
      <form onSubmit={handle} noValidate className="flex flex-col gap-3">
        <h1 className="text-xl font-semibold">{title}</h1>
        {notice && (
          <p role="status" className="text-sm text-success">
            {notice}
          </p>
        )}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="username">Username</Label>
          <Input
            id="username"
            name="username"
            ref={usernameRef}
            spellCheck={false}
            aria-invalid={invalid === "username"}
            aria-describedby={invalid === "username" ? errorId : undefined}
            autoComplete="username"
            autoCapitalize="none"
            required
            value={username}
            onChange={(e) => setUsername(e.target.value)}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="password">Password</Label>
          <Input
            id="password"
            name="password"
            ref={passwordRef}
            aria-invalid={invalid === "password"}
            aria-describedby={invalid === "password" ? errorId : undefined}
            type="password"
            autoComplete={title === "Log in" ? "current-password" : "new-password"}
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        {error && (
          <p id={errorId} role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        <Button type="submit" disabled={busy}>
          {submitLabel}
        </Button>
        <p className="text-xs text-muted-foreground">{footer}</p>
      </form>
    </Card>
  )
}

export function FormLink({ to, children }: { to: string; children: ReactNode }) {
  return (
    <Link to={to} className="text-primary underline underline-offset-2">
      {children}
    </Link>
  )
}
