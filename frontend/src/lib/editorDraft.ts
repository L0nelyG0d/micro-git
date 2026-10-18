/** Unsaved editor input, kept in sessionStorage so it survives a redirect to /login. */
export type EditorDraft = { text: string; path: string; message: string }

export const draftKey = (owner: string, repo: string, ref: string, mode: string, path: string) =>
  `micro-git:draft:${[owner, repo, ref, mode, path].map(encodeURIComponent).join("/")}`

export function loadDraft(key: string): EditorDraft | null {
  try {
    const raw = sessionStorage.getItem(key)
    if (!raw) return null
    const d = JSON.parse(raw) as Partial<EditorDraft>
    if (typeof d.text !== "string") return null
    return { text: d.text, path: String(d.path ?? ""), message: String(d.message ?? "") }
  } catch {
    return null
  }
}

export function saveDraft(key: string, draft: EditorDraft) {
  try {
    sessionStorage.setItem(key, JSON.stringify(draft))
  } catch {
    /* storage unavailable or full: the draft is simply not kept */
  }
}

export function clearDraft(key: string) {
  try {
    sessionStorage.removeItem(key)
  } catch {
    /* ignore */
  }
}
