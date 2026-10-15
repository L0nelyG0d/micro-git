import { useCallback, useEffect, useState } from "react"
import { api, ApiError } from "@/lib/api"

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
 */
export function useFetch<T>(path: string | null): FetchState<T> {
  const [attempt, setAttempt] = useState(0)
  const [settled, setSettled] = useState<Settled<T>>()

  useEffect(() => {
    if (path === null) return
    let active = true
    api<T>(path)
      .then((data) => active && setSettled({ path, attempt, data }))
      .catch((error) => active && setSettled({ path, attempt, error }))
    return () => {
      active = false
    }
  }, [path, attempt])

  const reload = useCallback(() => setAttempt((n) => n + 1), [])
  const samePath = settled?.path === path
  const current = samePath && settled?.attempt === attempt
  return {
    data: samePath ? settled?.data : undefined,
    error: current ? settled?.error : undefined,
    loading: path !== null && !current,
    reload,
  }
}

/** User-facing text for a failed request. */
export function errorMessage(e: unknown): string {
  return e instanceof ApiError
    ? e.message
    : "Could not reach the server. Check your connection and try again."
}
