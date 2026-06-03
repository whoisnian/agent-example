/** Login mutation. `meta.silent` + the api's `toastOnError:false` make the
 *  login page the single error surface. `retry:false` is the mutation default
 *  but pinned here so a credential 401 is never retried (see design S1). */
import { useMutation, type UseMutationResult } from "@tanstack/react-query";
import { ApiError } from "@/services/http";
import { login } from "./api";
import type { LoginRequest, LoginResponse } from "./types";

export function useLoginMutation(): UseMutationResult<LoginResponse, ApiError, LoginRequest> {
  return useMutation({
    mutationFn: login,
    meta: { silent: true },
    retry: false,
  });
}
