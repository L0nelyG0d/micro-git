# Process

Tasks are GitHub issues in `L0nelyG0d/micro-git`. `_docs/tasks.md` is the backlog and the source of truth: issue numbers match task numbers (task 4 = issue #4). If the two disagree, tell the user and update the issue with `gh issue edit`.

## Roles

The main session is the orchestrator. It launches the roles below as subagents and does not groom, implement, or test itself.

- System analyst - reviews the backlog against `plan.md`, writes API contracts, resolves contradictions, follows `_docs/team/system-analyst.md`. Used once up front and then on demand
- PM - grooms a task, follows `_docs/team/pm.md`
- Engineer - implements one groomed task, follows `_docs/team/software-engineer.md`
- QA - checks the result against the acceptance criteria, follows `_docs/team/qa-engineer.md`
- Code reviewer - reviews the code after QA PASS, then merges into `main` and pushes, follows `_docs/team/code-reviewer.md`

Models (user decision 2026-10-19): PM and QA run on Haiku 5.5 (`haiku`); the system analyst, engineer and code reviewer run on Sonnet 5.5 (`sonnet`). At most 3 subagents at once. Tests run against the local Postgres on port 54329 (see AGENTS.md), not Neon.

## Lifecycle

1. Pick the next open issue from the backlog, in order (later tasks depend on earlier ones)
2. PM grooms it
3. Engineer creates the branch `issue-<N>-<slug>` from `main`, implements it there, commits on that branch only, and hands over to QA by commenting the branch name and head commit on the issue
4. If the task contradicts itself or `plan.md`, work with the system analyst and the PM to find a suitable solution (architectural changes go to the user). If the solution was not found leave it at the mock stage comment on the issue and continue working
5. QA checks out the issue's branch and verifies it against the acceptance criteria there (tests run on the branch, not on `main`)
6. On FAIL, back to step 3 with the QA comment as input
7. On QA PASS, the orchestrator hands the issue to the code reviewer (the QA comment names the branch and commit). The reviewer reviews the diff and posts APPROVE or CHANGES REQUESTED
8. On CHANGES REQUESTED, back to step 3 with the review comment as input; because the code changed, QA re-checks (step 5) before the reviewer looks again
9. On APPROVE, the reviewer merges the branch into `main` (`git merge --no-ff issue-<N>-<slug>`, message `Merge #<N>`), runs the suite once on `main`, deletes the branch, pushes `main`, and comments the merge commit on the issue. If the merge conflicts or the suite goes red on `main`, the merge is undone and the issue goes back to the engineer to rebase the branch on `main`; QA and the reviewer then check again
10. QA confirms the merge on `main` and closes the issue (`mvp` and `user-test-bug` issues are closed by QA)
11. Repeat until the backlog is empty


## Controller role

The orchestrator is a controller: it assigns issues to branches, launches engineers, QA and the code reviewer, and keeps `_docs/questions.md`. It does not write feature code, verify features, review code, merge or push itself; the code reviewer merges and pushes, QA closes. Independent issues may run in parallel, each on its own branch (at most 5 agents at once); issues that touch the same files or depend on each other run in order. The reviewer merges one branch at a time, and the waiting branches are updated from `main` before QA re-checks them.

## Rules

- Do not skip step 2. A dependent issue starts only after its dependency is merged and closed; independent issues may run in parallel on separate branches
- The engineer does not close the issue and does not merge. QA does not fix code, it only outputs PASS or FAIL. The code reviewer does not fix code, it only outputs APPROVE or CHANGES REQUESTED
- Only the code reviewer merges into `main` and pushes. The review starts only after a QA PASS comment, and any new commit on the branch needs a new QA PASS
- Close an issue only after QA outputs PASS, the reviewer APPROVED and merged, and the tests are green: `go test ./...` and `go test -race ./...` for backend work, `npm test` for frontend work
- If an issue is unclear or conflicts with `_docs/plan.md`, ask the user before building
- If a decision needs the user (architecture, data model, stack, endpoint set) and they are away, do not wait: append the question to `_docs/questions.md` with the default you used, take the simplest reversible default, comment on the issue, and keep going. The user reviews that file on return
- When a user decision is pending and the user is present, ask and wait about one minute. No answer: stash the question as above and keep working. Never stop the pipeline to wait

## CI and QA

`.github/workflows/ci.yml` runs the repeatable mechanical gates on every push to `main`, every pull request and on demand: gofmt, `go vet`, the backend suite (`go test -p 1 -race ./...` against a throwaway Postgres service container, never Neon; the same suite also runs with `TEST_STORE=sqlite` and `TEST_STORE=memory` in a parallel job with no database), the frontend tests and build, `openapi.yaml` lint, and a secret scan. It needs no repository secrets. QA owns what CI cannot: acceptance criteria, exploratory and adversarial testing, browser and visual checks, and review of the tests. QA no longer re-runs gofmt, vet and the unit suites as its main evidence; it may rely on a green CI run plus its own targeted checks.

## Testing-phase issues

QA turns every finding into a new GitHub issue (use `_docs/task_template.md`, add the next number to `tasks.md`, label UI findings `ui`). These issues follow the normal lifecycle, plus:

- UI issues use the project skills in `.claude/skills` (pinned in `skills-lock.json`): the PM and engineer invoke `web-design-guidelines` to audit the affected screens, then `design-taste-frontend` and `image-to-code` to implement the fix. QA re-audits the result with `web-design-guidelines`
- The skills never override `_docs/design-system.md` or the AGENTS.md rule for design questions: show the user several options before changing the look
- Non-UI issues (API, data, CLI) need none of these skills

## Dogfooding with mg

Our own work is also committed to micro git with the `mg` CLI (`bin/mg`, built from `backend/cmd/mg`). Real `git` stays the source of truth and is the only thing pushed to GitHub, and only when the user asks.

1. After the reviewer's APPROVE and before the merge, the code reviewer runs (server and local DB up, `MG_PASSWORD` in the environment, never in a doc or commit):
   - `bin/mg login <user>` (once per session; `bin/mg new mg-self` once to create the repository)
   - `bin/mg commit <user>/mg-self -m "<message> (Refs #N)" -b issue-N-slug <files:path>...`, listing the files the branch changed (`git diff --name-only main...HEAD`; use `-rm path` for deleted files). Or keep a working copy: `mg clone <user>/mg-self ../mg-self-copy`, sync the changed files in, then `mg push -m "..."`
2. `mg commit` prints the short hash; get the full one with `bin/mg log <user>/mg-self`
3. Store it: comment `branch`, `git head` and `mg` hashes on the issue (`gh issue comment N`) and add a line `mg: <hash>` under the issue's section in `_docs/tasks.md`
4. A failed `mg commit` (stale tip, server down) does not block the merge: note it on the issue and retry later
