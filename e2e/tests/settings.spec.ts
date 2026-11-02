import { expect, test } from "@playwright/test"
import { createRepo, logOut, signUp, uniqueName } from "../support/helpers"

test("change a public repository to private from settings", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo)

  await page.goto(`/${user}/${repo}/settings`)
  await expect(page.getByText("This repository is currently public.")).toBeVisible()
  await page.getByRole("button", { name: "Change visibility", exact: true }).click()
  await page.getByRole("alertdialog").getByRole("button", { name: "Make private", exact: true }).click()
  await expect(page.getByText("This repository is currently private.")).toBeVisible()
})

test("a private repository is hidden from an anonymous visitor", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo, { isPrivate: true })
  await logOut(page)

  await page.goto(`/${user}/${repo}`)
  await expect(page.getByRole("heading", { name: "Repository not found", exact: true })).toBeVisible()
})

test("a public repository is readable by an anonymous visitor without Settings", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo)
  await logOut(page)

  await page.goto(`/${user}/${repo}`)
  await expect(page.getByRole("heading", { name: "Page not found", exact: true })).toHaveCount(0)
  await expect(page.getByRole("navigation", { name: "Repository" }).getByRole("link", { name: "Settings" })).toHaveCount(0)
})
