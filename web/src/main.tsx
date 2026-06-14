import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClientProvider } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import { RouterProvider } from "react-router-dom";

import { ErrorBoundary } from "@/components/feedback/ErrorBoundary";
import { Toaster } from "@/components/feedback/Toaster";
import { createAppRouter } from "@/router";
import { createQueryClient } from "@/services/query-client";
import { setNavigator } from "@/services/http";
import { setRealtimeNavigator } from "@/services/ws";
import { installRealtimeGapFill } from "@/features/tasks/use-task-live";
import { initThemeSystemSync } from "@/features/theme/store";

import "@/styles/globals.css";

const queryClient = createQueryClient();
const router = createAppRouter();

// Inject a navigator so services that live outside React can trigger
// route changes (e.g. apiFetch on 401, realtime client on 4001).
const navigate = (to: string): void => {
  void router.navigate(to);
};
setNavigator(navigate);
setRealtimeNavigator(navigate);

// Register the realtime gap-fill handler once (id-based event backfill into
// the React Query cache); avoids a per-page setRealtimeOnGap that remounts
// would clobber.
installRealtimeGapFill(queryClient);

// Follow OS color-scheme changes while the theme preference is `system`. The
// FOUC-safe boot script in index.html already applied the initial class.
initThemeSystemSync();

const rootEl = document.getElementById("root");
if (!rootEl) {
  throw new Error("Missing #root in index.html");
}

ReactDOM.createRoot(rootEl).render(
  <React.StrictMode>
    <ErrorBoundary>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
        <Toaster />
        {import.meta.env.DEV ? <ReactQueryDevtools initialIsOpen={false} /> : null}
      </QueryClientProvider>
    </ErrorBoundary>
  </React.StrictMode>,
);
