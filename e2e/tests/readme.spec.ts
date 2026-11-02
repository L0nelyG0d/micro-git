import { expect, test } from "@playwright/test"
import { commitNewFile, createRepo, signUp, uniqueName } from "../support/helpers"

test("the root README.md is rendered on the repository page", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo)
  await commitNewFile(page, user, repo, "main", "README.md", "# Hello E2E\n\nSome **bold** text.\n", "readme")

  await page.goto(`/${user}/${repo}`)
  await expect(page.getByRole("heading", { name: "Hello E2E", exact: true })).toBeVisible()
  await expect(page.locator("strong", { hasText: "bold" })).toBeVisible()
  await expect(page.getByRole("heading", { name: "README.md", exact: true })).toBeVisible()
})
