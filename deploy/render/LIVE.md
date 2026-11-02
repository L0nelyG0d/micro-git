# Live deployment

- Service: Render web service `micro-git`, built from the root `Dockerfile`
- URL: https://micro-git.onrender.com
- Health: `GET /health` (also `GET /api/health`)
- Deployed by the Deploy workflow through the `RENDER_DEPLOY_HOOK` secret on each green `main` commit

Refs #130, #132
