/** Auth DTOs — mirror the `api-auth` `POST /api/v1/auth/login` contract. */

/** The authenticated principal returned by login (never includes secrets). */
export interface AuthUser {
  id: string;
  tenant_id: string;
  email: string;
}

export interface LoginRequest {
  email: string;
  password: string;
}

export interface LoginResponse {
  /** Signed HS256 JWT. */
  token: string;
  /** RFC3339 UTC instant the token stops being valid. */
  expires_at: string;
  user: AuthUser;
}
