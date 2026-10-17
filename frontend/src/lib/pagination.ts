/** Reads a `?page=` value; anything but an integer >= 1 falls back to page 1. */
export function parsePage(raw: string | null): number {
  const n = Number(raw)
  return Number.isInteger(n) && n >= 1 ? n : 1
}
