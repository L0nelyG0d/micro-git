import { expect, test } from "@playwright/test"
import { commitNewFile, createRepo, signUp, uniqueName } from "../support/helpers"

test("commit history lists commits newest first", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo)
  await commitNewFile(page, user, repo, "main", "README.md", "# hi\n", "first commit")
  await commitNewFile(page, user, repo, "main", "docs/notes.txt", "notes\n", "second commit")

  await page.goto(`/${user}/${repo}/commits/main`)
  const rows = page.getByRole("row")
  await expect(rows.nth(1)).toContainText("second commit")
  await expect(rows.nth(2)).toContainText("first commit")
})

test("a commit page shows the changed file", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo)
  await commitNewFile(page, user, repo, "main", "README.md", "# hi\n", "first commit")
  await commitNewFile(page, user, repo, "main", "docs/notes.txt", "notes\n", "second commit")

  await page.goto(`/${user}/${repo}/commits/main`)
  await page.getByRole("row").nth(1).getByRole("link").first().click()
  await expect(page).toHaveURL(/\/commit\/[0-9a-f]+$/)
  await expect(page.getByText("docs/notes.txt").first()).toBeVisible()
})
