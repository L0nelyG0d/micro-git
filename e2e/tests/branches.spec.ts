import { expect, test } from "@playwright/test"
import { commitNewFile, createBranch, createRepo, signUp, uniqueName } from "../support/helpers"

test("create a branch from the repository page", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo)
  await commitNewFile(page, user, repo, "main", "README.md", "# hi\n", "first commit")
  await createBranch(page, user, repo, "feature")
})

test("delete a branch from settings", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo)
  await commitNewFile(page, user, repo, "main", "README.md", "# hi\n", "first commit")
  await createBranch(page, user, repo, "feature")

  await page.goto(`/${user}/${repo}/settings`)
  await expect(page.getByRole("link", { name: "feature", exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Delete branch", exact: true }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Delete feature" }).click()
  await page.getByRole("alertdialog").getByRole("button", { name: "Delete branch", exact: true }).click()
  await expect(page.getByRole("link", { name: "feature", exact: true })).toHaveCount(0)
  await expect(page.getByRole("link", { name: "main", exact: true })).toBeVisible()
})
