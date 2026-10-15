import { useState } from "react"
import type { FormEvent, ReactNode } from "react"
import { Link } from "react-router-dom"
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

export function AuthForm({ title, submitLabel, notice, footer, onSubmit }: Props) {
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)

  async function handle(e: FormEvent) {
    e.preventDefault()
    if (busy) return
    setBusy(true)
    setError("")
    try {
      await onSubmit(username, password)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong. Try again.")
      setPassword("") // the password is always cleared after a failure
      setBusy(false)
    }
  }

  return (
    <Card className="mx-auto w-full max-w-sm p-3">
      <form onSubmit={handle} className="flex flex-col gap-3">
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
            type="password"
            autoComplete={title === "Log in" ? "current-password" : "new-password"}
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        {error && (
          <p role="alert" className="text-sm text-destructive">
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
