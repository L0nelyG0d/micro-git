import { Navigate, useNavigate } from "react-router-dom"
import { useAuth } from "@/auth/AuthContext"
import { apiService } from "@/services"
import { AuthForm, FormLink } from "./AuthForm"

export function Register() {
  const { user, login } = useAuth()
  const navigate = useNavigate()
  if (user) return <Navigate to="/" replace />
  return (
    <AuthForm
      title="Register"
      submitLabel="Register"
      onSubmit={async (username, password) => {
        // Usernames are lower-cased by the server; no client-side rejection.
        await apiService.register(username, password)
        try {
          await login(username, password)
        } catch {
          navigate("/login", {
            replace: true,
            state: { notice: "Your account was created. Log in to continue." },
          })
        }
      }}
      footer={
        <>
          Already have an account? <FormLink to="/login">Log in</FormLink>
        </>
      }
    />
  )
}
