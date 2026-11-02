import { expect, test } from "@playwright/test"
import { commitNewFile, createRepo, signUp, uniqueName } from "../support/helpers"

test("commit a new file through the web editor", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  const message = `add hello ${repo}`
  await signUp(page, user)
  await createRepo(page, repo)
  await commitNewFile(page, user, repo, "main", "hello.txt", "hello\nworld\n", message)
  await expect(page.getByText(message).first()).toBeVisible()
})

test("edit an existing file and commit the change", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo)
  await commitNewFile(page, user, repo, "main", "hello.txt", "one\n", "first commit")

  await page.goto(`/${user}/${repo}/blob/main/hello.txt`)
  await page.getByRole("link", { name: "Edit", exact: true }).click()
  await page.getByLabel("File content", { exact: true }).fill("two\n")
  await page.getByLabel("Commit message", { exact: true }).fill("edit hello")
  await page.getByRole("button", { name: "Commit", exact: true }).click()
  await expect(page).toHaveURL(/\/commit\/[0-9a-f]+$/)
  await expect(page.getByText("edit hello").first()).toBeVisible()
})

test("delete a file through the editor", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo)
  await commitNewFile(page, user, repo, "main", "hello.txt", "bye\n", "first commit")

  await page.goto(`/${user}/${repo}/edit/main/hello.txt`)
  await page.getByLabel("Commit message", { exact: true }).fill("remove hello")
  await page.getByRole("button", { name: "Delete file", exact: true }).click()
  await page.getByRole("alertdialog").getByRole("button", { name: "Delete file", exact: true }).click()
  await expect(page).toHaveURL(/\/commit\/[0-9a-f]+$/)
  await page.goto(`/${user}/${repo}`)
  await expect(page.getByRole("link", { name: "hello.txt", exact: true })).toHaveCount(0)
})

test("the editor refuses a commit message over 500 characters", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo)
  await page.goto(`/${user}/${repo}/new/main`)
  await page.getByLabel("File path", { exact: true }).fill("long.txt")
  await page.getByLabel("File content", { exact: true }).fill("x\n")
  await page.getByLabel("Commit message", { exact: true }).fill("m".repeat(501))
  await expect(page.getByText("Commit message must be at most 500 characters.")).toBeVisible()
  await expect(page.getByRole("button", { name: "Commit", exact: true })).toBeDisabled()
})
