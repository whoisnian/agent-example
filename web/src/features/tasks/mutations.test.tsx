/** Invalidation contract: task mutations refresh the list prefix on settle so
 *  the TaskList page and the nav Recents reflect lifecycle changes. */
import type { JSX, ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useIterateTaskMutation } from "./mutations";
import { taskKeys } from "./queries";

describe("task mutations invalidation", () => {
  it("iterate onSettled invalidates detail, versions, and the list prefix", async () => {
    const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
    const spy = vi.spyOn(qc, "invalidateQueries");
    const wrapper = ({ children }: { children: ReactNode }): JSX.Element => (
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    );

    const { result } = renderHook(() => useIterateTaskMutation(), { wrapper });
    result.current.mutate({ taskId: "task-1", body: { prompt: "again" } });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(spy).toHaveBeenCalledWith({ queryKey: taskKeys.detail("task-1") });
    expect(spy).toHaveBeenCalledWith({ queryKey: taskKeys.versions("task-1") });
    expect(spy).toHaveBeenCalledWith({ queryKey: taskKeys.lists });
  });
});
