import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { VisibilityDialog } from "@/components/VisibilityDialog"
import { cacheClear } from "@/lib/fetchCache"
import { installMock } from "@/test-utils"
import { MockApiService } from "@/services/MockApiService"

type Res = { status: number; body?: unknown }
let respond: () => Res | Promise<Res>
let requests: { url: string; method?: string; body: unknown }[]
const onChanged = vi.fn()
const onUnauthorized = vi.fn()

beforeEach(() => {
  cacheClear()
  requests = []
  onChanged.mockClear()
  onUnauthorized.mockClear()
  respond = () => ({ status: 200, body: {} })
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      requests.push({ url, method: init?.method, body: init?.body ? JSON.parse(init.body as string) : undefined })
      const r = await respond()
      return { status: r.status, ok: r.status < 300, json: async () => r.body } as Response
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

function renderDialog(isPrivate: boolean) {
  render(
    <VisibilityDialog owner="alice" repo="hello" isPrivate={isPrivate} onChanged={onChanged} onUnauthorized={onUnauthorized} />,
  )
}
const open = () => userEvent.click(screen.getByRole("button", { name: "Change visibility" }))

/** alice owns alice/hello (private or not) and is logged in. */
async function ownedRepo(isPrivate: boolean) {
  const mock = new MockApiService()
  await mock.register("alice", "password1")
  await mock.login("alice", "password1")
  await mock.createRepo({ name: "hello", private: isPrivate })
  return installMock(mock)
}

describe("VisibilityDialog", () => {
  it.each([
    { isPrivate: false, title: "Make alice/hello private?", button: "Make private" },
    { isPrivate: true, title: "Make alice/hello public?", button: "Make public" },
  ])("when isPrivate=$isPrivate it asks $title and flips the visibility", async ({ isPrivate, title, button }) => {
    const mock = await ownedRepo(isPrivate)
    renderDialog(isPrivate)
    await open()
    expect(await screen.findByRole("alertdialog", { name: title })).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: button }))
    await vi.waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1))
    expect((await mock.getRepo("alice", "hello")).private).toBe(!isPrivate)
  })

  it("closes the dialog on success", async () => {
    await ownedRepo(false)
    renderDialog(false)
    await open()
    await userEvent.click(await screen.findByRole("button", { name: "Make private" }))
    await vi.waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
    expect(onChanged).toHaveBeenCalledTimes(1)
    expect(onUnauthorized).not.toHaveBeenCalled()
  })

  it("shows 'Saving…' on a disabled button while the request is pending", async () => {
    let finish!: (r: Res) => void
    respond = () => new Promise<Res>((resolve) => (finish = resolve))
    renderDialog(false)
    await open()
    await userEvent.click(await screen.findByRole("button", { name: "Make private" }))
    const saving = await screen.findByRole("button", { name: "Saving…" })
    expect(saving).toBeDisabled()
    expect(saving).toHaveAttribute("aria-busy", "true")
    finish({ status: 200, body: {} })
    await vi.waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1))
  })

  it("shows a server error inline, stays open and re-enables the button", async () => {
    respond = () => ({ status: 500, body: { error: "disk on fire" } })
    renderDialog(false)
    await open()
    await userEvent.click(await screen.findByRole("button", { name: "Make private" }))
    expect(await screen.findByRole("alert")).toHaveTextContent("disk on fire")
    expect(screen.getByRole("alertdialog")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Make private" })).toBeEnabled()
    expect(onChanged).not.toHaveBeenCalled()
    expect(onUnauthorized).not.toHaveBeenCalled()
  })

  it("calls onUnauthorized once on 401 and also shows the error", async () => {
    const mock = await ownedRepo(true)
    renderDialog(true)
    await open()
    await mock.logout() // the session ends behind the page, like another tab logging out
    await userEvent.click(await screen.findByRole("button", { name: "Make public" }))
    expect(await screen.findByRole("alert")).toHaveTextContent("authentication required")
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
    expect(onChanged).not.toHaveBeenCalled()
  })

  it("Cancel closes the dialog and leaves the repository as it was", async () => {
    const mock = await ownedRepo(false)
    renderDialog(false)
    await open()
    await userEvent.click(await screen.findByRole("button", { name: "Cancel" }))
    await vi.waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
    expect((await mock.getRepo("alice", "hello")).private).toBe(false)
    expect(onChanged).not.toHaveBeenCalled()
  })
})
