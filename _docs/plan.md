# micro git — plan

## What it is

micro git is a small git-hosting site. Users create repositories, commit file changes from the browser, and browse branches, history, files and diffs. The backend mimics git's content-addressed storage (blob / tree / commit objects and branch refs). The frontend mimics a GitHub repository page.

Not a goal: the clone/push/fetch protocol. Everything happens through the web UI and the JSON API.

## Core model

- Objects use git's exact serialization and SHA-1 (`crypto/sha1`): `oid = sha1("<type> <len>\0" + body)`.
  - blob: the file bytes
  - tree: entries sorted by name, each `<mode> <name>\0<20-byte oid>` (`100644` file, `40000` directory)
  - commit: `tree <oid>`, zero or more `parent <oid>`, `author`, `committer`, blank line, message
- Because the format is git's own, oids match `git hash-object`. Test vectors: empty blob `e69de29bb2d1d6434b8b29ae775ad8c2e48c5391`; `"test content\n"` → `d670460b4b4aece5915caf5c68d12f560a9fe3e4`.
- Objects are immutable and de-duplicated per repo (`INSERT ... ON CONFLICT DO NOTHING`).
- A branch is a ref: name → commit oid. Committing = write the blobs, rebuild only the changed trees bottom-up, write the commit, then move the ref with a compare-and-swap on the expected tip (`409` if it moved).
- Branch names match `[A-Za-z0-9._-]+` and contain no `/`, so `/tree/{ref}/{path...}` is unambiguous.

## Features

MVP:

1. Register, log in, log out (username + password, JWT in an httpOnly cookie)
2. Create, list, view and delete repositories. Anyone can read; only the owner can write
3. Commit from the browser: create or edit files, delete files, message, target branch, several files per commit
4. Browse: file tree at a branch or commit, file viewer, branch switcher
5. History: commit list per branch (first-parent walk, paginated) and commit detail with a per-file line diff
6. Branches: list, create from another branch, delete (not the default one)

Stretch, cut in this order if late: fast-forward merge of one branch into another; compare two branches; render `README.md`; rename a repository.

Out of scope: clone/push/fetch, pack files, real merges and conflict resolution, tags, forks, pull requests, issues, SSH keys, binary files, private repositories, organizations.

## Tables (`backend/db/schema.sql`)

| table | columns |
|---|---|
| `users` | `id`, `username` (unique case-insensitively, `[a-z0-9-]{3,32}`), `password_hash`, `created_at` |
| `repos` | `id`, `owner_id` → users, `name` (unique per owner, case-insensitive), `description`, `default_branch` (default `main`), `created_at` |
| `objects` | `repo_id` → repos (on delete cascade), `oid` char(40), `type` (`blob`/`tree`/`commit`, CHECK), `data` bytea (raw object body); primary key `(repo_id, oid)` |
| `refs` | `repo_id` → repos (on delete cascade), `name`, `oid`; primary key `(repo_id, name)` |

Objects are stored as raw serialized bodies (like an uncompressed loose object) and parsed in Go. There are no separate commits or trees tables: the log walks parents, 50 commits per page.

## Endpoints

Routing uses `net/http` patterns. Errors are JSON `{"error": "..."}`.

| endpoint | auth | notes |
|---|---|---|
| `GET /health` | none | `200 OK` |
| `POST /auth/register` | none | `201`; `409` username taken; `400` password shorter than 8 or bad username |
| `POST /auth/login` | none | sets the `session` cookie; token never in the body |
| `POST /auth/logout` | cookie | clears the cookie |
| `GET /auth/me` | cookie | `{id, username}` |
| `GET /repos` | none | optional `?owner=` |
| `POST /repos` | yes | `{name, description}`; `409` duplicate; the repo starts empty with no branches |
| `GET /repos/{owner}/{repo}` | none | includes `default_branch` and an `empty` flag |
| `DELETE /repos/{owner}/{repo}` | owner | `403` for anyone else |
| `GET /repos/{owner}/{repo}/branches` | none | name + tip oid |
| `POST /repos/{owner}/{repo}/branches` | owner | `{name, from}`; `409` if it exists |
| `DELETE /repos/{owner}/{repo}/branches/{branch}` | owner | `400` for the default branch |
| `GET /repos/{owner}/{repo}/commits?ref=&page=` | none | oid, message, author, time |
| `GET /repos/{owner}/{repo}/commits/{oid}` | none | changed files + diff hunks against the first parent |
| `GET /repos/{owner}/{repo}/tree/{ref}/{path...}` | none | directory entries; `ref` is a branch name or a commit oid |
| `GET /repos/{owner}/{repo}/blob/{ref}/{path...}` | none | file content and size |
| `POST /repos/{owner}/{repo}/commits` | owner | `{branch, expected_tip, message, changes: [{path, content} or {path, delete: true}]}`; `409` if the tip moved |

## Rules

- Paths: no leading `/`, no `.` / `..` / empty segments, no `.git` segment, at most 255 characters. A path cannot be both a file and a directory (`400`)
- Limits: file at most 1 MiB; UTF-8 text only (`400` otherwise); at most 50 changes per commit; message 1–500 characters; repo name `[A-Za-z0-9._-]{1,100}`
- A commit that changes nothing (same content, or deleting a missing file) returns `400` "no changes"
- Deleting the last file in a directory removes the directory (git has no empty trees)
- The first commit on an empty repo has no parent and creates the branch; `expected_tip` is empty then
- The author is the logged-in user. Timestamps come from an injected clock so tests are deterministic
- Diff: line-based (LCS), computed on the server. Each file has a `status` (`added`, `modified`, `deleted`) and hunks of `+`, `-` and context lines
- Every write endpoint: no cookie → `401`; not the owner → `403`

## Frontend

Styling follows `_docs/design-system.md`.

| route | page |
|---|---|
| `/login`, `/register` | auth forms |
| `/` | repository list and "New repository" |
| `/:owner/:repo` | file tree, branch switcher, latest commit bar |
| `/:owner/:repo/blob/:ref/*` | file viewer; "Edit" for the owner |
| `/:owner/:repo/edit/:ref/*` | editor and commit message form |
| `/:owner/:repo/commits/:ref` | history |
| `/:owner/:repo/commit/:oid` | commit detail with diff |

Every page has loading, empty and error states. Oids show as 7 characters. Dates are `DD.MM.YYYY HH:mm`.

## One-week schedule

| day | goal |
|---|---|
| 1 | Go and React skeletons, schema on Neon, auth (register, login, JWT) |
| 2 | Object store: hashing, blob/tree/commit serialize and parse, golden-vector tests (pure Go, no database) |
| 3 | Repo endpoints; commit-writing endpoint (tree rebuild, ref compare-and-swap) |
| 4 | Read endpoints: tree, blob, commit log, branches |
| 5 | Commit detail and diff; frontend shell, auth pages, repo list |
| 6 | Repo page, file viewer, history, diff view, editor and commit form |
| 7 | Stretch (fast-forward merge) only if everything is green; polish, README, demo script, final QA |

## Risks

- Tree rebuild (nested paths, deletes, empty directories) is the hardest part. Keep it a pure function and test it without the database
- Hashing must match real git. Verify with the vectors above before building on it
