import type { JSX } from "react";
import { NavLink } from "react-router-dom";
import { cn } from "@/lib/cn";
import { Skeleton } from "@/components/ui/skeleton";
import { useTasksQuery } from "@/features/tasks/queries";

/** First page of the list read doubles as "recents" (created_at descending —
 *  recently *created*, not recently active; see the change design). */
const RECENTS_PAGE_SIZE = 8;

/** Status → dot color, mirroring the StatusBadge variant mapping. */
const STATUS_DOT: Record<string, string> = {
  running: "bg-primary",
  paused: "bg-warning",
  cancelling: "bg-warning",
  succeeded: "bg-success",
  failed: "bg-destructive",
};

/**
 * Recent-tasks section of the left navigation column. Quiet by design: the
 * read is silent at both toast layers and loading/empty/error all render as
 * inline placeholders — a broken nav list must not surface global errors.
 */
export function RecentTasks(): JSX.Element {
  const query = useTasksQuery({ page: 1, pageSize: RECENTS_PAGE_SIZE }, { silent: true });

  return (
    <div
      className="flex min-h-0 flex-1 flex-col overflow-y-auto px-2 pb-2"
      data-testid="recent-tasks"
    >
      <span className="px-3 pb-1 pt-2 text-xs font-medium text-muted-foreground">Recents</span>
      {query.isPending ? (
        <div data-testid="recent-tasks-loading" className="flex flex-col gap-2 px-3 py-1">
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-4/5" />
          <Skeleton className="h-4 w-3/5" />
        </div>
      ) : query.data ? (
        query.data.items.length === 0 ? (
          <p data-testid="recent-tasks-empty" className="px-3 py-1 text-xs text-muted-foreground">
            No tasks yet.
          </p>
        ) : (
          <ul className="flex flex-col gap-0.5">
            {query.data.items.map((t) => (
              <li key={t.id}>
                <NavLink
                  to={`/tasks/${t.id}`}
                  data-testid="recent-task-item"
                  title={t.title}
                  className={({ isActive }): string =>
                    cn(
                      "flex items-center gap-2 rounded-md px-3 py-1.5 text-sm transition-colors",
                      isActive
                        ? "bg-accent text-accent-foreground"
                        : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
                    )
                  }
                >
                  <span
                    aria-hidden
                    className={cn(
                      "size-1.5 shrink-0 rounded-full",
                      STATUS_DOT[t.status] ?? "bg-muted-foreground",
                    )}
                  />
                  <span className="truncate">{t.title}</span>
                </NavLink>
              </li>
            ))}
          </ul>
        )
      ) : (
        <p data-testid="recent-tasks-error" className="px-3 py-1 text-xs text-muted-foreground">
          Recent tasks unavailable.
        </p>
      )}
    </div>
  );
}
