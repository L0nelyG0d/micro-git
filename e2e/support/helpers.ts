import { expect, type Page } from "@playwright/test"

/** Password that satisfies the backend rule (8 to 72 bytes). */
export const PASSWORD = "correct-horse-battery-1"

/**
 * A lower-case name that is unique per call (timestamp + random suffix).
 * Valid as a username (a-z, 0-9, "-", 3 to 32 chars) and as a repository name.
 */
export function uniqueName(prefix: string): string {
  const stamp = Date.now().toString(36)
  const rand = Math.random().toString(36).slice(2, 7)
  return `${prefix}-${stamp}${rand}`.toLowerCase()
}

/** Registers through the UI and ends signed in on "/". */
export async function signUp(page: Page, username: string, password = PASSWORD): Promise<void> {
  await page.goto("/register")
  await page.getByLabel("Username", { exact: true }).fill(username)
  await page.getByLabel("Password", { exact: true }).fill(password)
  await page.getByRole("button", { name: "Register", exact: true }).click()
  await expect(page.getByRole("button", { name: "Log out", exact: true })).toBeVisible()
}

export async function logIn(page: Page, username: string, password = PASSWORD): Promise<void> {
  await page.goto("/login")
  await page.getByLabel("Username", { exact: true }).fill(username)
  await page.getByLabel("Password", { exact: true }).fill(password)
  await page.getByRole("button", { name: "Log in", exact: true }).click()
  await expect(page.getByRole("button", { name: "Log out", exact: true })).toBeVisible()
}

export async function logOut(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Log out", exact: true }).click()
  await expect(page.getByRole("link", { name: "Log in", exact: true })).toBeVisible()
}

/** Creates a repository from the home page form and waits until it is listed. */
export async function createRepo(
  page: Page,
  name: string,
  opts: { description?: string; isPrivate?: boolean } = {},
): Promise<void> {
  await page.goto("/")
  await page.getByRole("button", { name: "New repository", exact: true }).click()
  const form = page.getByRole("form", { name: "New repository" })
  await form.getByLabel("Name", { exact: true }).fill(name)
  if (opts.description) await form.getByLabel(/^Description/).fill(opts.description)
  if (opts.isPrivate) await form.getByRole("radio", { name: "Private" }).check()
  await form.getByRole("button", { name: "Create repository", exact: true }).click()
  await expect(page.getByRole("link", { name: new RegExp(`(^|/)${name}$`) })).toBeVisible()
}

/** Commits one new file through the web editor (new file route) and waits for the commit page. */
export async function commitNewFile(
  page: Page,
  owner: string,
  repo: string,
  branch: string,
  path: string,
  content: string,
  message: string,
): Promise<void> {
  await page.goto(`/${owner}/${repo}/new/${branch}`)
  await page.getByLabel("File path", { exact: true }).fill(path)
  await page.getByLabel("File content", { exact: true }).fill(content)
  await page.getByLabel("Commit message", { exact: true }).fill(message)
  await page.getByRole("button", { name: "Commit", exact: true }).click()
  await expect(page).toHaveURL(/\/commit\/[0-9a-f]+$/)
}

/** Creates a branch from the repository page ("Create branch" dialog) and waits for its tree page. */
export async function createBranch(page: Page, owner: string, repo: string, name: string): Promise<void> {
  await page.goto(`/${owner}/${repo}`)
  await page.getByRole("button", { name: "Create branch", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Branch name").fill(name)
  await dialog.getByRole("button", { name: "Create branch", exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/${owner}/${repo}/tree/${name}$`))
}
