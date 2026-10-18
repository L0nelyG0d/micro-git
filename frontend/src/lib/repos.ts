export type Repo = {
  id: number
  owner: string
  name: string
  description: string
  default_branch: string
  private: boolean
  empty: boolean
  created_at: string
}

/** Response of `GET /repos`. */
export type RepoList = {
  repos: Repo[]
  page: number
  per_page: number
  has_more: boolean
}

export const REPO_NAME_RE = /^[A-Za-z0-9._-]{1,100}$/
export const DESCRIPTION_MAX = 500

/** Returns an inline error message, or "" when the name is valid. */
export function validateRepoName(name: string): string {
  if (!name) return "Enter a repository name."
  if (name === "." || name === "..") return "Name cannot be . or .."
  if (!REPO_NAME_RE.test(name))
    return "Use 1-100 letters, digits, dots, hyphens or underscores."
  return ""
}

export function validateDescription(description: string): string {
  return description.length > DESCRIPTION_MAX
    ? `Description must be at most ${DESCRIPTION_MAX} characters.`
    : ""
}
