import type { ApiService } from "./ApiService"
import { HttpApiService } from "./HttpApiService"

export { ApiError } from "./http"
export type { ApiService, CommitInput, RepoPatch } from "./ApiService"

/**
 * The one service instance the UI uses. Only this file names the implementations.
 * With VITE_USE_MOCK=true it is the in-browser MockApiService, loaded by a dynamic
 * import so a build without the flag contains no mock code (Vite replaces the
 * flag with a constant and drops the branch).
 */
export const apiService: ApiService =
  import.meta.env.VITE_USE_MOCK === "true"
    ? new (await import("./MockApiService")).MockApiService()
    : new HttpApiService()
