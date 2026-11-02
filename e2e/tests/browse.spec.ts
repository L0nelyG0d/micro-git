import { expect, test } from "@playwright/test"
import { commitNewFile, createRepo, signUp, uniqueName } from "../support/helpers"

test("the repository page lists files and folders of the branch", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo)
  await commitNewFile(page, user, repo, "main", "README.md", "# hi\n", "first commit")
  await commitNewFile(page, user, repo, "main", "docs/notes.txt", "notes\n", "add notes")

  await page.goto(`/${user}/${repo}`)
  await expect(page.getByRole("link", { name: "README.md", exact: true }).first()).toBeVisible()
  await expect(page.getByRole("link", { name: "docs", exact: true }).first()).toBeVisible()
})

test("open a folder, then a file, and read its content", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo)
  await commitNewFile(page, user, repo, "main", "docs/notes.txt", "remember the milk\n", "add notes")

  await page.goto(`/${user}/${repo}`)
  await page.getByRole("link", { name: "docs", exact: true }).first().click()
  await expect(page).toHaveURL(new RegExp(`/${user}/${repo}/tree/main/docs$`))
  await page.getByRole("link", { name: "notes.txt", exact: true }).first().click()
  await expect(page).toHaveURL(new RegExp(`/${user}/${repo}/blob/main/docs/notes.txt$`))
  await expect(page.getByText("remember the milk")).toBeVisible()
})
