import { expect, test } from "@playwright/test"
import { createRepo, signUp, uniqueName } from "../support/helpers"

test("create a repository from the home page", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo, { description: "created by e2e" })
  await page.goto(`/${user}/${repo}`)
  await expect(page).toHaveURL(new RegExp(`/${user}/${repo}$`))
  await expect(page.getByText("Public", { exact: true }).first()).toBeVisible()
})

test("the list shows own public and private repositories with badges", async ({ page }) => {
  const user = uniqueName("e2e")
  const pub = uniqueName("pub")
  const priv = uniqueName("priv")
  await signUp(page, user)
  await createRepo(page, pub)
  await createRepo(page, priv, { isPrivate: true })
  await page.goto("/")
  const list = page.getByRole("list", { name: "Repository list" })
  await expect(list.getByRole("link", { name: new RegExp(`(^|/)${pub}$`) })).toBeVisible()
  await expect(list.getByRole("link", { name: new RegExp(`(^|/)${priv}$`) })).toBeVisible()
  // The home page lists every public repository, so check the badge inside each of this user's rows.
  const row = (name: string) => list.getByRole("listitem").filter({ has: page.getByRole("link", { name: new RegExp(`(^|/)${name}$`) }) })
  await expect(row(pub).getByText("Public", { exact: true })).toHaveCount(1)
  await expect(row(priv).getByText("Private", { exact: true })).toHaveCount(1)
})

test("delete a repository from settings", async ({ page }) => {
  const user = uniqueName("e2e")
  const repo = uniqueName("repo")
  await signUp(page, user)
  await createRepo(page, repo)
  await page.goto(`/${user}/${repo}/settings`)
  await page.getByRole("button", { name: "Delete repository", exact: true }).click()
  await page.getByRole("alertdialog").getByRole("button", { name: "Delete repository", exact: true }).click()
  await expect(page).toHaveURL("/")
  await expect(page.getByRole("link", { name: new RegExp(`(^|/)${repo}$`) })).toHaveCount(0)
  await page.goto(`/${user}/${repo}`)
  await expect(page.getByRole("heading", { name: "Repository not found", exact: true })).toBeVisible()
})
