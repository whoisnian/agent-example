import type { JSX } from "react";
import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Button } from "@/components/primitives/Button";
import { StatusBadge } from "@/components/tasks/StatusBadge";
import { CostBadge } from "@/components/tasks/CostBadge";
import { useTasksQuery } from "@/features/tasks/queries";
import { TASK_STATUSES } from "@/features/tasks/types";

const PAGE_SIZE = 20;

export function TaskList(): JSX.Element {
  const navigate = useNavigate();
  const [page, setPage] = useState(1);
  const [status, setStatus] = useState<string>("all");

  const query = useTasksQuery({
    page,
    pageSize: PAGE_SIZE,
    status: status === "all" ? undefined : status,
  });

  const data = query.data;
  const totalPages = data ? Math.max(1, Math.ceil(data.total / data.page_size)) : 1;

  return (
    <section data-testid="task-list-page">
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-2xl font-semibold text-text">Tasks</h1>
        <Link to="/tasks/new">
          <Button data-testid="new-task-button">New task</Button>
        </Link>
      </div>

      <div className="mb-4">
        <label className="text-sm text-text-muted">
          Status{" "}
          <select
            data-testid="status-filter"
            value={status}
            onChange={(e) => {
              setStatus(e.target.value);
              setPage(1);
            }}
            className="rounded border border-border bg-surface px-2 py-1 text-sm text-text"
          >
            <option value="all">all</option>
            {TASK_STATUSES.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        </label>
      </div>

      {query.isPending ? (
        <p data-testid="task-list-loading" className="text-sm text-text-muted">
          Loading…
        </p>
      ) : data && data.items.length === 0 ? (
        <p data-testid="task-list-empty" className="text-sm text-text-muted">
          No tasks yet. Create one to get started.
        </p>
      ) : (
        <table data-testid="task-list-table" className="w-full text-left text-sm">
          <thead className="text-text-muted">
            <tr>
              <th className="py-2">Title</th>
              <th className="py-2">Type</th>
              <th className="py-2">Status</th>
              <th className="py-2">Cost</th>
            </tr>
          </thead>
          <tbody>
            {data?.items.map((t) => (
              <tr
                key={t.id}
                data-testid="task-row"
                onClick={() => navigate(`/tasks/${t.id}`)}
                className="cursor-pointer border-t border-border hover:bg-surface"
              >
                <td className="py-2 text-text">{t.title}</td>
                <td className="py-2 text-text-muted">{t.task_type}</td>
                <td className="py-2">
                  <StatusBadge status={t.status} />
                </td>
                <td className="py-2">
                  <CostBadge cost={t.cost} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <div className="mt-4 flex items-center gap-2 text-sm text-text-muted">
        <Button
          variant="ghost"
          disabled={page <= 1}
          onClick={() => setPage((p) => Math.max(1, p - 1))}
          data-testid="page-prev"
        >
          Prev
        </Button>
        <span data-testid="page-indicator">
          Page {data?.page ?? page} / {totalPages}
        </span>
        <Button
          variant="ghost"
          disabled={page >= totalPages}
          onClick={() => setPage((p) => p + 1)}
          data-testid="page-next"
        >
          Next
        </Button>
      </div>
    </section>
  );
}
