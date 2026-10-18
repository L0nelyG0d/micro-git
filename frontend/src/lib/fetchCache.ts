/**
 * Small in-memory GET cache keyed by API path (stale-while-revalidate is done
 * by useFetch). Writes through the service drop the affected entries.
 */
const MAX_ENTRIES = 200
const entries = new Map<string, unknown>()
let version = 0
const listeners = new Set<() => void>()

function notify(): void {
  for (const l of [...listeners]) l()
}

/** Called whenever entries are added or dropped (for useSyncExternalStore). */
export function cacheSubscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/** Presence check that does not touch the LRU order. */
export function cacheHas(path: string): boolean {
  return entries.has(path)
}

/** A 40-hex oid in the URL (tree/blob/commit or `?ref=`) names immutable content. */
export function isImmutablePath(path: string): boolean {
  return /\/(tree|blob|commits)\/[0-9a-f]{40}(\/|\?|$)/.test(path) || /[?&]ref=[0-9a-f]{40}(&|$)/.test(path)
}

export function cacheGet<T>(path: string): { data: T } | undefined {
  if (!entries.has(path)) return undefined
  const data = entries.get(path) as T
  entries.delete(path) // re-insert: most recently used goes last
  entries.set(path, data)
  return { data }
}

/** Token to pass back to cacheSet, so answers that raced a write are not stored. */
export function cacheToken(): number {
  return version
}

export function cacheSet(path: string, data: unknown, token: number): void {
  if (token !== version) return
  entries.delete(path)
  entries.set(path, data)
  if (entries.size > MAX_ENTRIES) entries.delete(entries.keys().next().value as string)
  notify()
}

export function cacheClear(): void {
  version++
  entries.clear()
  notify()
}

/** Drop everything cached for the repo behind `/repos/{owner}/{repo}...` and the repo lists. */
function dropRepo(prefix: string): void {
  version++
  for (const key of [...entries.keys()]) {
    const isRepoKey = key === prefix || key.startsWith(prefix + "/") || key.startsWith(prefix + "?")
    if (isRepoKey || key === "/repos" || key.startsWith("/repos?")) entries.delete(key)
  }
  notify()
}

/** Called after every non-GET request. */
export function invalidateAfterWrite(path: string): void {
  const m = /^\/repos\/[^/?]+\/[^/?]+/.exec(path)
  if (m) dropRepo(m[0])
  else cacheClear() // /auth/* (the user changes) and POST /repos
}
