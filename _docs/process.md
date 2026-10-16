# Process

Tasks are GitHub issues in `L0nelyG0d/micro-git`. `_docs/tasks.md` is the backlog and the source of truth: issue numbers match task numbers (task 4 = issue #4). If the two disagree, tell the user and update the issue with `gh issue edit`.

## Roles

The main session is the orchestrator. It launches the roles below as subagents and does not groom, implement, or test itself.

- System analyst - reviews the backlog against `plan.md`, writes API contracts, resolves contradictions, follows `_docs/team/system-analyst.md`. Used once up front and then on demand
- PM - grooms a task, follows `_docs/team/pm.md`
- Engineer - implements one groomed task, follows `_docs/team/software-engineer.md`
- QA - checks the result against the acceptance criteria, follows `_docs/team/qa-engineer.md`

## Lifecycle

1. Pick the next open issue from the backlog, in order (later tasks depend on earlier ones)
2. PM grooms it
3. Engineer creates the branch `issue-<N>-<slug>` from `main`, implements it there, commits on that branch only, and hands over to QA by commenting the branch name and head commit on the issue
4. If the task contradicts itself or `plan.md`, work with the system analyst and the PM to find a suitable solution (architectural changes go to the user). If the solution was not found leave it at the mock stage comment on the issue and continue working
5. QA checks out the issue's branch and verifies it against the acceptance criteria there (tests run on the branch, not on `main`)
6. On FAIL, back to step 3 with the QA comment as input
7. On PASS, the orchestrator merges the branch into `main` (`git merge --no-ff issue-<N>-<slug>`, message `Merge #<N>`, `Closes #<N>`), runs the suite once on `main`, deletes the branch, and closes the issue. If the merge conflicts or the suite goes red on `main`, the issue goes back to the engineer to rebase the branch on `main` and QA re-checks
8. Repeat until the backlog is empty


## Controller role

The orchestrator is a controller: it assigns issues to branches, launches engineers and QA, merges QA-approved branches, closes issues and keeps `_docs/questions.md`. It does not write feature code or verify features itself. Independent issues may run in parallel, each on its own branch (at most 5 agents at once); issues that touch the same files or depend on each other run in order. Merge one branch at a time, and update the waiting branches from `main` before QA re-checks them.

## Rules

- Do not skip step 2. A dependent issue starts only after its dependency is merged and closed; independent issues may run in parallel on separate branches
- The engineer does not close the issue. QA does not fix code, it only outputs PASS or FAIL
- Close an issue only after QA outputs PASS with the tests green: `go test ./...` and `go test -race ./...` for backend work, `npm test` for frontend work
- If an issue is unclear or conflicts with `_docs/plan.md`, ask the user before building
- If a decision needs the user (architecture, data model, stack, endpoint set) and they are away, do not wait: append the question to `_docs/questions.md` with the default you used, take the simplest reversible default, comment on the issue, and keep going. The user reviews that file on return
- When a user decision is pending and the user is present, ask and wait about one minute. No answer: stash the question as above and keep working. Never stop the pipeline to wait
