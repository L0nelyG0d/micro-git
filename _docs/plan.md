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
| `refs` | `repo_id` → repos (on delete cascade), `name`, `oid`; primary key `(repo_id, name)`; foreign key `(repo_id, oid)` → objects, so a branch can only point at a stored object (write the commit object before moving the ref) |

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

Clarifications (system analyst, resolve gaps in the rules above):

- Usernames are lower-cased on register and on login, then matched against `[a-z0-9-]{3,32}`; `api` is reserved (it collides with the dev proxy prefix, `400`). Owner lookup in URLs is case-insensitive
- Password: 8–72 bytes (bcrypt ignores or rejects more)
- Repo name additionally cannot be `.` or `..`. Description at most 500 characters
- Branch name additionally cannot be `.` or `..`, is at most 100 characters, and cannot look like an oid (40 lowercase hex characters), so a `ref` is never ambiguous. A `ref` is resolved as a branch name first, otherwise as a full 40-character commit oid of this repo. Abbreviated oids are not accepted by the API (the UI shows 7 characters but links with the full oid)
- Commit `message` is trimmed; length is checked after trimming; stored with one trailing `\n`. Author and committer line: `<username> <username@micro-git.local> <unix seconds> +0000`
- `changes`: an entry is an upsert (`path` + `content`, content may be empty) or a delete (`path` + `delete: true`). Both fields, or neither, is `400`. The same path twice in one request is `400`. A delete of a missing path is ignored; a delete of a directory path is `400`. "no changes" is returned only when the whole commit changes nothing. `content` containing a NUL character is not text (`400`)
- Request body larger than 64 MiB: `413`
- The first commit on an empty repo must target `default_branch` (`400` otherwise). On a non-empty repo, `branch` must already exist (`404`); new branches are only created through the branches endpoint
- `expected_tip` must be the branch's current tip. Empty string is valid only when the branch does not exist yet; any other mismatch (including a non-empty tip on an empty repo, or an empty one on an existing branch) is `409`
- `empty` on a repo means it has no refs
- Order of checks on write endpoints: `401` (no/invalid cookie) → `404` (repo or branch missing) → `403` (not owner) → `400` (validation) → `409` (conflict)
- Reading from an empty repo (tree, blob, commits) returns `404` "ref not found"; the frontend uses the `empty` flag and does not call these
- Diff: 3 lines of context; hunks whose context overlaps are merged; the text is split on `\n`; a missing final newline is not shown as a difference (a file that differs only by it is `modified` with no hunks). A file with more than 5000 lines on either side is not diffed: `hunks: []`, `truncated: true`. No rename detection

## API contracts

Conventions (all endpoints):

- JSON in and out, UTF-8, `snake_case`, `Content-Type: application/json`. Exception: `GET /health` returns plain text `OK`. No `/api` prefix on the backend (the Vite proxy adds and strips it)
- Error body is always `{"error": "<message>"}` with the message exactly as written below. Extra field only on the tip conflict (`current_tip`). Malformed JSON, wrong types or unknown fields: `400` `invalid JSON body`. Unknown route `404` `not found`; wrong method `405` `method not allowed`; unexpected failure `500` `internal error` (no details leaked)
- Missing/invalid/expired cookie, or a cookie for a user that no longer exists: `401` `authentication required`. Not the owner: `403` `forbidden`
- Ids are integers. Times are RFC 3339 UTC strings (`2026-01-02T15:04:05Z`); the frontend formats them. Oids are full 40-character lowercase hex
- Session cookie: `session=<jwt>; Path=/; HttpOnly; SameSite=Lax; Max-Age=604800`, plus `Secure` when env `COOKIE_SECURE=true`. JWT claims: `sub` (user id), `exp`
- `{path...}` may be empty (repo root) and may have a trailing slash; register the pattern with and without the trailing part. Path segments are percent-decoded and then validated by the path rules. Literal `.`, `..` and empty segments in a request URL are redirected (`307`) by ServeMux to the cleaned path and never reach the handler
- Lists are wrapped in an object, never a bare array

Shared shapes:

```
Repo   = {"id": 1, "owner": "alice", "name": "hello", "description": "", "default_branch": "main",
          "empty": true, "created_at": "2026-01-02T15:04:05Z"}
Branch = {"name": "main", "oid": "<40 hex>", "is_default": true}
```

### Auth

`GET /health` → `200` `OK`

`POST /auth/register`
- Request: `{"username": "alice", "password": "correct horse"}`
- `201` `{"id": 1, "username": "alice"}` (no cookie is set; the client calls login next)
- `400` `username must be 3-32 characters: a-z, 0-9 and -` · `username is reserved` · `password must be 8-72 bytes` · `invalid JSON body`
- `409` `username already taken`

`POST /auth/login`
- Request: `{"username": "alice", "password": "correct horse"}`
- `200` `{"id": 1, "username": "alice"}` and `Set-Cookie: session=...` (token never in the body)
- `400` `username and password are required`
- `401` `invalid username or password` (same message for unknown user and wrong password)

`POST /auth/logout`
- No body. `204`, clears the cookie (`Max-Age=0`). Idempotent: works without a cookie too (no `401`)

`GET /auth/me`
- `200` `{"id": 1, "username": "alice"}`
- `401` `authentication required`

### Repositories

`GET /repos` and `GET /repos?owner=alice`
- `200` `{"repos": [Repo, ...]}`, newest first, no pagination. Unknown owner gives `{"repos": []}`

`POST /repos` (auth)
- Request: `{"name": "hello", "description": "optional"}` (`description` may be omitted)
- `201` Repo (`empty: true`, `default_branch: "main"`)
- `400` `invalid repository name` · `description must be at most 500 characters`
- `401` · `409` `repository already exists`

`GET /repos/{owner}/{repo}` → `200` Repo · `404` `repository not found`

`DELETE /repos/{owner}/{repo}` (owner) → `204` · `401` · `403` · `404` `repository not found`

### Branches

`GET /repos/{owner}/{repo}/branches`
- `200` `{"branches": [Branch, ...]}` sorted by name; `{"branches": []}` for an empty repo
- `404` `repository not found`

`POST /repos/{owner}/{repo}/branches` (owner)
- Request: `{"name": "feature", "from": "main"}` (`from` is an existing branch name)
- `201` Branch (`oid` equals the source tip)
- `400` `invalid branch name` · `401` · `403`
- `404` `repository not found` · `branch not found` (source missing, includes an empty repo)
- `409` `branch already exists`

`DELETE /repos/{owner}/{repo}/branches/{branch}` (owner)
- `204` · `400` `cannot delete the default branch` · `401` · `403` · `404` `branch not found`

### Reading

`GET /repos/{owner}/{repo}/tree/{ref}/{path...}`
- `200`
```
{"ref": "main", "commit": "<40 hex>", "path": "src",
 "entries": [{"name": "lib", "type": "tree", "oid": "<40 hex>"},
             {"name": "main.go", "type": "blob", "oid": "<40 hex>"}]}
```
  `path` is `""` for the root. `commit` is the resolved commit (the editor uses it as `expected_tip` when `ref` is a branch). Entries: directories first, then files, each sorted by name (bytewise). No size or last-commit info per entry
- `400` `invalid path` · `not a directory` (path is a file)
- Unclean URL paths: net/http's ServeMux cleans literal `.`, `..` and empty (`//`) segments and answers `307` to the cleaned path before any handler runs, so those never produce 400. The handler returns `400 invalid path` only for what reaches it: a `.git` segment, over 255 characters, percent-encoded dot segments (`%2e%2e`, `%2F..%2F`)
- `404` `repository not found` · `ref not found` · `path not found`

`GET /repos/{owner}/{repo}/blob/{ref}/{path...}`
- `200`
```
{"ref": "main", "commit": "<40 hex>", "path": "src/main.go", "name": "main.go",
 "oid": "<40 hex>", "size": 12, "content": "package main\n"}
```
  `size` is bytes; `content` is the UTF-8 text (only text is ever stored)
- `400` `invalid path` (same rules and the same 307 for unclean paths as tree) · `not a file` (path is a directory or the root)
- `404` `repository not found` · `ref not found` · `path not found`

`GET /repos/{owner}/{repo}/commits?ref=main&page=1`
- `ref` defaults to `default_branch`; `page` is 1-based, default 1. First-parent walk from the ref, 50 per page
- `200`
```
{"ref": "main", "page": 1, "per_page": 50, "has_more": true,
 "commits": [{"oid": "<40 hex>", "message": "Add readme", "author": "alice",
              "time": "2026-01-02T15:04:05Z", "parents": ["<40 hex>"]}]}
```
  `message` is the full message without the trailing newline; the UI shows the first line. A page past the end gives `commits: []`, `has_more: false`
- `400` `invalid page` · `404` `repository not found` · `ref not found`

`GET /repos/{owner}/{repo}/commits/{oid}`
- `200`
```
{"oid": "<40 hex>", "tree": "<40 hex>", "parents": ["<40 hex>"], "message": "Add readme",
 "author": "alice", "time": "2026-01-02T15:04:05Z",
 "files": [{"path": "README.md", "status": "modified", "additions": 1, "deletions": 1, "truncated": false,
            "hunks": [{"old_start": 1, "old_lines": 3, "new_start": 1, "new_lines": 3,
                       "lines": [{"type": "context", "text": "# Title"},
                                 {"type": "del", "text": "old line"},
                                 {"type": "add", "text": "new line"}]}]}]}
```
  `status` is `added` | `modified` | `deleted`; line `type` is `context` | `add` | `del`; `text` has no `+`/`-` prefix and no newline. For `added` files `old_start` and `old_lines` are 0; for `deleted`, `new_start` and `new_lines` are 0. Files sorted by path. A root commit has `parents: []` and diffs against the empty tree. Diffs are against `parents[0]`
- `400` `invalid commit id` (not 40 hex) · `404` `repository not found` · `commit not found` (missing, or the oid is not a commit)

### Writing commits

`POST /repos/{owner}/{repo}/commits` (owner)
- Request:
```
{"branch": "main", "expected_tip": "<40 hex, or \"\" for the first commit>", "message": "Add readme",
 "changes": [{"path": "README.md", "content": "# Hi\n"}, {"path": "old.txt", "delete": true}]}
```
- `201` `{"oid": "<40 hex>", "tree": "<40 hex>", "parent": "<40 hex or null>", "branch": "main"}`
- `400`: `message must be 1-500 characters` · `changes must contain 1-50 entries` · `invalid branch name` · `invalid expected_tip` · `invalid path: <path>` · `duplicate path: <path>` · `change must have content or delete: <path>` · `file too large: <path>` · `file is not valid UTF-8 text: <path>` · `path conflicts with existing file or directory: <path>` · `first commit must target the default branch` · `no changes`
- `401` · `403` · `404` `repository not found` · `branch not found`
- `409` `branch tip has moved` with the extra field `"current_tip": "<40 hex or empty string if the branch does not exist>"`
- `413` `request too large`

## Frontend

Styling follows `_docs/design-system.md`.

| route | page |
|---|---|
| `/login`, `/register` | auth forms |
| `/` | repository list and "New repository" |
| `/:owner/:repo` | file tree, branch switcher, latest commit bar |
| `/:owner/:repo/blob/:ref/*` | file viewer; "Edit" for the owner |
| `/:owner/:repo/edit/:ref/*` | editor and commit message form for an existing file |
| `/:owner/:repo/new/:ref` | editor for a new file (path field); linked from an "Add file" button on the repo page (owner only) |
| `/:owner/:repo/commits/:ref` | history |
| `/:owner/:repo/commit/:oid` | commit detail with diff |

"Edit", "Add file" and "Delete file" are shown only to the owner and only when `ref` is a branch name (a commit oid is read-only). The owner is detected by comparing `GET /auth/me` `username` with the `:owner` route param. Every page has loading, empty and error states. Oids show as 7 characters. Dates are `DD.MM.YYYY HH:mm`.

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
