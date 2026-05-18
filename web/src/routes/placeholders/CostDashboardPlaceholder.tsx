import type { JSX } from "react";
export function CostDashboardPlaceholder(): JSX.Element {
  return (
    <section data-testid="placeholder-cost">
      <h1 className="mb-2 text-2xl font-semibold text-text">Cost</h1>
      <p className="text-sm text-text-muted">
        Cost dashboard — not implemented in this scaffold.
      </p>
    </section>
  );
}
