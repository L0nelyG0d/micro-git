export class ApiError extends Error {
  status: number
  /** Whole seconds from the Retry-After header (429 answers), if present. */
  retryAfter?: number
  constructor(status: number, message: string, retryAfter?: number) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.retryAfter = retryAfter
  }
}

/**
 * Calls the backend through the /api proxy with the session cookie.
 * Returns the parsed JSON body (undefined for 204). Non-2xx responses throw
 * ApiError carrying the server's `error` message; network failures throw the
 * original TypeError from fetch.
 */
export async function api<T = unknown>(
  path: string,
  options: { method?: string; body?: unknown } = {},
): Promise<T> {
  const init: RequestInit = {
    method: options.method ?? "GET",
    credentials: "include",
  }
  if (options.body !== undefined) {
    init.headers = { "Content-Type": "application/json" }
    init.body = JSON.stringify(options.body)
  }
  const res = await fetch(`/api${path}`, init)
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
    throw new ApiError(res.status, msg, Number.isInteger(ra) && ra > 0 ? ra : undefined)
  }
  return data as T
}
