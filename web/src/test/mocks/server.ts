import { setupServer } from "msw/node";
import { handlers } from "./handlers";

/**
 * Shared msw server instance for unit tests. Started in `setup.ts` once per
 * vitest worker; handlers reset between tests via `server.resetHandlers()`.
 */
export const server = setupServer(...handlers);
