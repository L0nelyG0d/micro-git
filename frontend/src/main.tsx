import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { createBrowserRouter, RouterProvider } from "react-router-dom"
import "./index.css"
import App from "./App.tsx"
import { pageRoutes } from "./routes.tsx"

// Data router: required for useBlocker (unsaved-changes guard in the editor).
// App is the layout route; the pages are its children (routes.tsx).
const router = createBrowserRouter([{ element: <App />, children: pageRoutes }])

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <RouterProvider router={router} />
  </StrictMode>,
)
