import { expect, test } from "@playwright/test"
import { commitNewFile, createBranch, createRepo, signUp, uniqueName } from "../support/helpers"

test("compare shows a file added on the feature branch", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo)
  await commitNewFile(page, user, repo, "main", "README.md", "# hi\n", "first commit")
  await createBranch(page, user, repo, "feature")
  await commitNewFile(page, user, repo, "feature", "docs/notes.txt", "notes\n", "feature commit")

  await page.goto(`/${user}/${repo}/compare?base=main&head=feature`)
  await expect(page.getByRole("heading", { name: "Compare branches", exact: true })).toBeVisible()
  await expect(page.getByText("docs/notes.txt").first()).toBeVisible()
})
