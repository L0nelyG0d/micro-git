import { expect, test } from "@playwright/test"
import { PASSWORD, logIn, logOut, signUp, uniqueName } from "../support/helpers"

test("register creates an account and signs in at once", async ({ page }) => {
  await signUp(page, uniqueName("e2e"))
  await expect(page).toHaveURL("/")
  await expect(page.getByRole("heading", { name: "Repositories", exact: true })).toBeVisible()
})

test("log in again with the same credentials after logging out", async ({ page }) => {
  const user = uniqueName("e2e")
  await signUp(page, user)
  await logOut(page)
  await logIn(page, user)
  await expect(page.getByRole("heading", { name: "Repositories", exact: true })).toBeVisible()
})

test("log out shows the guest links again", async ({ page }) => {
  await signUp(page, uniqueName("e2e"))
  await page.getByRole("button", { name: "Log out", exact: true }).click()
  await expect(page.getByRole("link", { name: "Log in", exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: "Register", exact: true }).first()).toBeVisible()
})

test("a wrong password is refused with an alert", async ({ page }) => {
  const user = uniqueName("e2e")
  await signUp(page, user)
  await logOut(page)
  await page.goto("/login")
  await page.getByLabel("Username", { exact: true }).fill(user)
  await page.getByLabel("Password", { exact: true }).fill(`${PASSWORD}-wrong`)
  await page.getByRole("button", { name: "Log in", exact: true }).click()
  await expect(page.getByRole("alert")).toBeVisible()
  await expect(page.getByRole("button", { name: "Log out", exact: true })).toHaveCount(0)
})
