import type { JSX } from "react";
import { NavLink } from "react-router-dom";

const items = [
  { to: "/tasks", label: "Tasks" },
  { to: "/cost", label: "Cost" },
  { to: "/settings", label: "Settings" },
] as const;

export function SideNav(): JSX.Element {
  return (
    <nav
      className="w-48 shrink-0 border-r border-border bg-surface p-3"
      aria-label="Primary"
      data-testid="side-nav"
    >
      <ul className="flex flex-col gap-1">
        {items.map((item) => (
          <li key={item.to}>
            <NavLink
              to={item.to}
              className={({ isActive }): string =>
                [
                  "block rounded px-3 py-2 text-sm",
                  isActive
                    ? "bg-accent text-white"
                    : "text-text-muted hover:bg-bg hover:text-text",
                ].join(" ")
              }
              data-testid={`nav-${item.label.toLowerCase()}`}
            >
              {item.label}
            </NavLink>
          </li>
        ))}
      </ul>
    </nav>
  );
}
