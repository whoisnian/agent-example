import type { JSX } from "react";
export function SettingsPlaceholder(): JSX.Element {
  return (
    <section data-testid="placeholder-settings">
      <h1 className="mb-2 text-2xl font-semibold text-text">Settings</h1>
      <p className="text-sm text-text-muted">
        Settings — not implemented in this scaffold.
      </p>
    </section>
  );
}
