import type { JSX } from "react";
export function NotFoundPlaceholder(): JSX.Element {
  return (
    <section data-testid="placeholder-not-found">
      <h1 className="mb-2 text-2xl font-semibold text-text">Not Found</h1>
      <p className="text-sm text-text-muted">Nothing here.</p>
    </section>
  );
}
