import { expect, test } from "@playwright/test"
import { createRepo, signUp, uniqueName } from "../support/helpers"

test("rename a repository from settings and open it under the new name", async ({ page }) => {
  const user = uniqueName("e2e")
  const oldName = uniqueName("old")
  const newName = uniqueName("new")
  await signUp(page, user)
  await createRepo(page, oldName)

  await page.goto(`/${user}/${oldName}/settings`)
  await page.getByRole("button", { name: "Rename", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Repository name").fill(newName)
  await dialog.getByRole("button", { name: "Rename repository", exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/${user}/${newName}/settings$`))

  await page.goto(`/${user}/${newName}`)
  await expect(page.getByRole("heading", { name: new RegExp(newName) }).first()).toBeVisible()
})

test("the old name is not found after a rename", async ({ page }) => {
  const user = uniqueName("e2e")
  const oldName = uniqueName("old")
  const newName = uniqueName("new")
  await signUp(page, user)
  await createRepo(page, oldName)

  await page.goto(`/${user}/${oldName}/settings`)
  await page.getByRole("button", { name: "Rename", exact: true }).click()
  await page.getByRole("dialog").getByLabel("Repository name").fill(newName)
  await page.getByRole("dialog").getByRole("button", { name: "Rename repository", exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/${user}/${newName}/settings$`))

  await page.goto(`/${user}/${oldName}`)
  await expect(page.getByRole("heading", { name: "Repository not found", exact: true })).toBeVisible()
})
