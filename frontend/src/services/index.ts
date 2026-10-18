import type { ApiService } from "./ApiService"
import { HttpApiService } from "./HttpApiService"

export { ApiError } from "./http"
export type { ApiService, CommitInput, RepoPatch } from "./ApiService"

/** The one service instance the UI uses. Only this file names the implementation. */
export const apiService: ApiService = new HttpApiService()
