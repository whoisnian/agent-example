import type { JSX } from "react";
export function SettingsPlaceholder(): JSX.Element {
  return (
    <section data-testid="placeholder-settings">
      <h1 className="mb-2 text-2xl font-semibold text-foreground">Settings</h1>
      <p className="text-sm text-muted-foreground">
        Settings — not implemented in this scaffold.
      </p>
    </section>
  );
}
