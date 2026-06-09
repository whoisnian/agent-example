import type { JSX } from "react";
export function NotFoundPlaceholder(): JSX.Element {
  return (
    <section data-testid="placeholder-not-found">
      <h1 className="mb-2 text-2xl font-semibold text-foreground">Not Found</h1>
      <p className="text-sm text-muted-foreground">Nothing here.</p>
    </section>
  );
}
