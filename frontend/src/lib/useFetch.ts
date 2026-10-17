import { useCallback, useEffect, useRef, useState } from "react"
import { api, ApiError } from "@/lib/api"
import { cacheGet, cacheSet, cacheToken, isImmutablePath } from "@/lib/fetchCache"

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

  useEffect(() => {
    if (path === null) return
    const force = forced.current
    forced.current = false
    if (!force && isImmutablePath(path) && cacheGet(path)) return
    const ctl = new AbortController()
    const token = cacheToken()
    api<T>(path, { signal: ctl.signal })
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
  }, [path, attempt])

  const reload = useCallback(() => {
    forced.current = true
    setAttempt((n) => n + 1)
  }, [])
  const samePath = settled?.path === path
  const current = samePath && settled?.attempt === attempt
  const cached = !samePath && path !== null ? cacheGet<T>(path) : undefined
  return {
    data: samePath ? settled?.data : cached?.data,
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
