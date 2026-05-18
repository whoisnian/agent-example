import { Navigate, useLocation } from "react-router-dom";
import type { ReactElement } from "react";
import { useAuthStore } from "@/features/auth/store";

/**
 * Gate the authenticated tree. Reads the token synchronously from the store.
 * No server validation — that's deferred to `add-web-auth`.
 */
export function RequireAuth({ children }: { children: ReactElement }): ReactElement {
  const token = useAuthStore((s) => s.token);
  const location = useLocation();
  if (!token) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  }
  return children;
}
