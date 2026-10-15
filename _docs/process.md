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
3. Engineer implements it
4. If the task contradicts itself or `plan.md`, work with the system analyst and the PM to find a suitable solution (architectural changes go to the user). If the solution was not found leave it at the mock stage comment on the issue and continue working
5. QA verifies it against the issue's acceptance criteria
6. On FAIL, back to step 3 with the QA comment as input
7. On PASS, the orchestrator closes the issue
8. Repeat until the backlog is empty


## Rules

- Do not skip step 2 or start the next issue before the current one is closed
- The engineer does not close the issue. QA does not fix code, it only outputs PASS or FAIL
- Close an issue only after QA outputs PASS with the tests green: `go test ./...` and `go test -race ./...` for backend work, `npm test` for frontend work
- If an issue is unclear or conflicts with `_docs/plan.md`, ask the user before building
