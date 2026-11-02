# Vercel (frontend only)

Vercel can host the React frontend (`frontend/`, Vite). It **cannot host this project's backend**:

- The Go API is a long-running server with state kept in its own store. It is not a fit for Vercel's per-request serverless functions, and the project has no Vercel-side config for it.
- Postgres is not provided by Vercel. The database has to live elsewhere (Neon, the VM in `deploy/gcp`, or the instance in `deploy/aws`).

So the split is: **Vercel serves the SPA, another host runs the Docker image** (Render or Railway from `deploy/`, or the GCP/AWS setups).

## How the frontend reaches the API

There is no `VITE_API_URL` in the frontend. `frontend/src/services/http.ts` calls `fetch("/api" + path)` with `credentials: "include"`. `frontend/vite.config.ts` proxies `/api` to the backend only in dev (`API_PROXY_TARGET`). In production the browser expects `/api` on the same origin as the page.

The fix that needs **no code change** is a Vercel rewrite that proxies `/api/*` to the backend. The browser still sees one origin, so the session cookie set by the backend behaves as first-party.

Create `frontend/vercel.json` (not in the repository yet):

```json
{
  "buildCommand": "npm run build",
  "outputDirectory": "dist",
  "rewrites": [
    { "source": "/api/:path*", "destination": "https://<backend-host>/:path*" },
    { "source": "/(.*)", "destination": "/index.html" }
  ]
}
```

- `<backend-host>` is the public URL of the backend (no trailing slash). The rewrite strips `/api`, as the Vite proxy does (`p.replace(/^\/api/, "")`).
- The SPA fallback keeps client-side routes working on refresh. The `/api` rule must stay first.
- Set the Vercel project's **Root Directory** to `frontend`.

Alternative, if the rewrite is not wanted: a `VITE_API_URL` prefix in `http.ts` (a small code change). Then the backend must allow the Vercel origin for CORS, and the session cookie must be `SameSite=None; Secure`. The rewrite avoids both.

The CI job `deploy-vercel-frontend` (`.github/workflows/deploy.yml`) publishes `frontend/` as-is. Until `frontend/vercel.json` with the `/api` rewrite exists, the deployed frontend calls `/api/...` on the Vercel domain and gets the SPA fallback or a 404, so the job only makes sense after the rewrite is added.

## Checklist

1. Deploy the backend from the root `Dockerfile` with Render or Railway (`deploy/render.yaml`, `deploy/railway.json`) and note its URL.
2. Create `frontend/vercel.json` as above with that URL.
3. Import the repository in Vercel with Root Directory `frontend`.
4. Log in on the Vercel URL and check in DevTools > Application > Cookies that the session cookie is set for the Vercel domain.

## Limits

Check Vercel's current Hobby (free) plan terms before relying on it for anything beyond a demo; the free plan is for non-commercial use.
