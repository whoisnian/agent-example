/** Thin `apiFetch` wrapper for the auth login endpoint. */
import { apiFetch } from "@/services/http";
import type { LoginRequest, LoginResponse } from "./types";

/**
 * Authenticate against `POST /api/v1/auth/login`.
 *
 * `interceptUnauthorized:false` so a `401 invalid_credentials` surfaces inline
 * instead of triggering the global clear-session + redirect; `toastOnError:false`
 * so the login page is the single error surface (paired with the mutation's
 * `meta:{silent:true}`). Body is stringified at the call site — `apiFetch` does
 * not serialize.
 */
export function login(body: LoginRequest): Promise<LoginResponse> {
  return apiFetch<LoginResponse>("/api/v1/auth/login", {
    method: "POST",
    body: JSON.stringify(body),
    toastOnError: false,
    interceptUnauthorized: false,
  });
}
