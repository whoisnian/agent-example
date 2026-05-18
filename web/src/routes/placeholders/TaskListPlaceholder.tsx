import type { JSX } from "react";
export function TaskListPlaceholder(): JSX.Element {
  return (
    <section data-testid="placeholder-tasks">
      <h1 className="mb-2 text-2xl font-semibold text-text">Tasks</h1>
      <p className="text-sm text-text-muted">
        TaskList view — not implemented in this scaffold.
      </p>
    </section>
  );
}
