/** Formats an RFC 3339 time as DD.MM.YYYY HH:mm in the viewer's time zone. */
export function formatDate(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  const p = (n: number) => String(n).padStart(2, "0")
  return `${p(d.getDate())}.${p(d.getMonth() + 1)}.${d.getFullYear()} ${p(d.getHours())}:${p(d.getMinutes())}`
}

/** Human-readable byte size: "12 bytes", "1.5 KB". */
export function formatSize(bytes: number): string {
  if (bytes === 1) return "1 byte"
  if (bytes < 1024) return `${bytes} bytes`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

/** First line of a commit message. */
export function firstLine(message: string): string {
  return message.split("\n", 1)[0]
}

/** Oids show as 7 characters. */
export function shortOid(oid: string): string {
  return oid.slice(0, 7)
}
