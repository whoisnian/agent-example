import type { JSX } from "react";
import { useParams } from "react-router-dom";

export function TaskDetailPlaceholder(): JSX.Element {
  const { id } = useParams<{ id: string }>();
  return (
    <section data-testid="placeholder-task-detail">
      <h1 className="mb-2 text-2xl font-semibold text-text">Task {id}</h1>
      <p className="text-sm text-text-muted">
        Task <span data-testid="task-id">{id}</span> detail — not implemented in this scaffold.
      </p>
    </section>
  );
}
