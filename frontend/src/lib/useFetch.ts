import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react"
import { apiService, ApiError } from "@/services"
import { cacheGet, cacheHas, cacheSet, cacheSubscribe, cacheToken, isImmutablePath } from "@/lib/fetchCache"

export type FetchState<T> = {
  data: T | undefined
  /** ApiError for HTTP failures, anything else is a network failure. */
  error: unknown
  loading: boolean
  reload: () => void
}

type Settled<T> = { path: string; attempt: number; data?: T; error?: unknown }

/**
 * GETs `path` (null = idle). Keeps the previous data for the same path while
 * reloading, so lists do not flash empty. Re-runs when `path` changes.
 *
 * Answers are cached by path: a cached path is shown at once and revalidated in
 * the background (never for immutable oid URLs). The request is aborted when
 * the path changes or the component unmounts; an aborted request sets nothing.
 */
export function useFetch<T>(path: string | null): FetchState<T> {
  const [attempt, setAttempt] = useState(0)
  const [settled, setSettled] = useState<Settled<T>>()
  const forced = useRef(false)
  // An immutable oid path that is cached needs no request; if the entry is
  // dropped while mounted (write, cacheClear) this flips and the effect fetches.
  const skip = useSyncExternalStore(
    cacheSubscribe,
    () => path !== null && isImmutablePath(path) && cacheHas(path),
  )

  useEffect(() => {
    if (path === null) return
    const force = forced.current
    forced.current = false
    if (!force && skip) return
    const ctl = new AbortController()
    const token = cacheToken()
    apiService.get<T>(path, { signal: ctl.signal })
      .then((data) => {
        if (ctl.signal.aborted) return
        cacheSet(path, data, token)
        setSettled({ path, attempt, data })
      })
      .catch((error) => {
        if (ctl.signal.aborted) return
        setSettled({ path, attempt, error })
      })
    return () => ctl.abort()
  }, [path, attempt, skip])

  const reload = useCallback(() => {
    forced.current = true
    setAttempt((n) => n + 1)
  }, [])
  const samePath = settled?.path === path
  const current = samePath && settled?.attempt === attempt
  const cached = path !== null ? cacheGet<T>(path) : undefined
  return {
    // A failed revalidation keeps showing the cached answer (error is still set).
    data: samePath && settled?.data !== undefined ? settled.data : cached?.data,
    error: current ? settled?.error : undefined,
    loading: path !== null && !current && !cached,
    reload,
  }
}

/** User-facing text for a failed request. */
export function errorMessage(e: unknown): string {
  return e instanceof ApiError
    ? e.message
    : "Could not reach the server. Check your connection and try again."
}
