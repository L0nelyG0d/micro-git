import { invalidateAfterWrite } from "@/lib/fetchCache"

export class ApiError extends Error {
  status: number
  /** Whole seconds from the Retry-After header (429 answers), if present. */
  retryAfter?: number
  /** `current_tip` of a 409 "branch tip has moved" answer, if present. */
  currentTip?: string
  constructor(status: number, message: string, retryAfter?: number, currentTip?: string) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.retryAfter = retryAfter
    this.currentTip = currentTip
  }
}

export type RequestOptions = { method?: string; body?: unknown; signal?: AbortSignal }

/**
 * The only place that calls fetch. Calls the backend through the /api proxy
 * with the session cookie. Returns the parsed JSON body (undefined for 204 or a
 * body that is not JSON). Non-2xx responses throw ApiError carrying the
 * server's `error` message; network failures throw the original TypeError.
 */
export async function request<T = unknown>(path: string, options: RequestOptions = {}): Promise<T> {
  const init: RequestInit = {
    method: options.method ?? "GET",
    credentials: "include",
  }
  if (options.signal) init.signal = options.signal
  if (options.body !== undefined) {
    init.headers = { "Content-Type": "application/json" }
    init.body = JSON.stringify(options.body)
  }
  let res: Response
  try {
    res = await fetch(`/api${path}`, init)
  } finally {
    // Dropped once the answer is in, success or not: a failed write may still have changed the repo.
    if (init.method !== "GET" && init.method !== "HEAD") invalidateAfterWrite(path)
  }
  if (res.status === 204) return undefined as T
  let data: unknown
  try {
    data = await res.json()
  } catch {
    data = undefined
  }
  if (!res.ok) {
    const msg =
      data && typeof data === "object" && "error" in data && typeof data.error === "string"
        ? data.error
        : "Something went wrong. Try again."
    const ra = Number(res.headers?.get?.("Retry-After"))
    const tip = data && typeof data === "object" && "current_tip" in data && typeof data.current_tip === "string" ? data.current_tip : undefined
    throw new ApiError(res.status, msg, Number.isInteger(ra) && ra > 0 ? ra : undefined, tip)
  }
  return data as T
}
