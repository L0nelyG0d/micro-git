import { Navigate, useLocation } from "react-router-dom"
import { useAuth } from "@/auth/AuthContext"
import { AuthForm, FormLink } from "./AuthForm"

export function Login() {
  const { user, login } = useAuth()
  const location = useLocation()
  const state = location.state as { from?: string; notice?: string } | null
  // Logged-in users (including right after a successful login) go back to
  // the page they came from, or home.
  if (user) return <Navigate to={state?.from ?? "/"} replace />
  return (
    <AuthForm
      title="Log in"
      submitLabel="Log in"
      notice={state?.notice}
      onSubmit={async (u, p) => {
        await login(u, p)
      }}
      footer={
        <>
          No account yet? <FormLink to="/register">Register</FormLink>
        </>
      }
    />
  )
}
