import { expect, test } from "@playwright/test"
import { commitNewFile, createBranch, createRepo, signUp, uniqueName } from "../support/helpers"

test("a fast-forward merge brings the feature branch into main", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo)
  await commitNewFile(page, user, repo, "main", "README.md", "# hi\n", "first commit")
  await createBranch(page, user, repo, "feature")
  await commitNewFile(page, user, repo, "feature", "docs/notes.txt", "notes\n", "feature commit")

  await page.goto(`/${user}/${repo}`)
  await page.getByRole("button", { name: "Merge", exact: true }).click()
  const dialog = page.getByRole("alertdialog")
  await dialog.getByRole("combobox", { name: "Merge from" }).click()
  await page.getByRole("option", { name: "feature", exact: true }).click()
  await dialog.getByRole("button", { name: "Merge", exact: true }).click()
  await expect(dialog).toBeHidden()

  await page.goto(`/${user}/${repo}/tree/main`)
  await expect(page.getByRole("link", { name: "docs", exact: true }).first()).toBeVisible()
})

test("the merge button stays disabled when both branches are the same", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo)
  await commitNewFile(page, user, repo, "main", "README.md", "# hi\n", "first commit")
  await createBranch(page, user, repo, "feature")

  await page.goto(`/${user}/${repo}`)
  await page.getByRole("button", { name: "Merge", exact: true }).click()
  const dialog = page.getByRole("alertdialog")
  // The dialog starts with two different branches; picking the target as the source disables the button.
  await dialog.getByRole("combobox", { name: "Merge from" }).click()
  await page.getByRole("option", { name: /^main/ }).click()
  await expect(dialog.getByRole("button", { name: "Merge", exact: true })).toBeDisabled()
})
