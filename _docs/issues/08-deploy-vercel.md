Title: Deploy: Vercel (frontend only limitation)
Labels: deploy

## Goal

The React frontend is deployed on Vercel. Vercel hosts only the static frontend; the Go backend and Postgres must be hosted elsewhere and the frontend must point to that backend.

## Acceptance criteria

- [ ] The frontend builds on Vercel from `frontend/` with the documented build command
- [ ] The API base URL is configured through a Vercel environment variable, not hard-coded
- [ ] Client-side routes resolve on refresh (SPA rewrite configured)
- [ ] Documented limitation: the backend is not deployed on Vercel, so the app is not fully working on Vercel alone
- [ ] Cookie-based auth across domains is either handled (CORS and SameSite settings) or documented as a known limit
- [ ] The README states this is a frontend-only deployment

## Out of scope

- Running the Go backend on Vercel

## Constraints

- Depends on a backend deploy target (Render, Railway or AWS) for the API
