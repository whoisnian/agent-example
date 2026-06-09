import type { JSX } from "react";
import { NavLink, useNavigate } from "react-router-dom";
import {
  Bot,
  DollarSign,
  ListTodo,
  LogOut,
  PanelLeftClose,
  PanelLeftOpen,
  Settings,
  type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/cn";
import { Button } from "@/components/ui/button";
import { useAuthStore } from "@/features/auth/store";
import { useUiStore } from "@/features/ui/store";

interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
}

const items: readonly NavItem[] = [
  { to: "/tasks", label: "Tasks", icon: ListTodo },
  { to: "/cost", label: "Cost", icon: DollarSign },
  { to: "/settings", label: "Settings", icon: Settings },
] as const;

/**
 * Left navigation column of the three-column shell. Holds the logo, primary
 * nav, a recent-tasks entry, and (folded in from the retired TopBar) the
 * authenticated user area + logout. Collapses to an icon rail via the UI store.
 */
export function SideNav(): JSX.Element {
  const navigate = useNavigate();
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);
  const collapsed = useUiStore((s) => s.navCollapsed);
  const toggleNav = useUiStore((s) => s.toggleNav);

  const onLogout = (): void => {
    logout();
    navigate("/login", { replace: true });
  };

  return (
    <nav
      className={cn(
        "flex shrink-0 flex-col border-r border-border bg-card transition-[width]",
        collapsed ? "w-16" : "w-60",
      )}
      aria-label="Primary"
      data-testid="side-nav"
    >
      {/* Brand + collapse toggle */}
      <div className="flex h-14 items-center gap-2 px-3">
        <div className="flex size-8 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground">
          <Bot className="size-5" aria-hidden />
        </div>
        {!collapsed && (
          <span className="flex-1 truncate text-sm font-semibold text-foreground">
            Agent Task Platform
          </span>
        )}
        <Button
          variant="ghost"
          size="icon"
          className="size-8 shrink-0"
          onClick={toggleNav}
          aria-label={collapsed ? "Expand navigation" : "Collapse navigation"}
          data-testid="nav-collapse-toggle"
        >
          {collapsed ? (
            <PanelLeftOpen className="size-4" aria-hidden />
          ) : (
            <PanelLeftClose className="size-4" aria-hidden />
          )}
        </Button>
      </div>

      {/* Primary nav */}
      <ul className="flex flex-1 flex-col gap-1 p-2">
        {items.map((item) => {
          const Icon = item.icon;
          return (
            <li key={item.to}>
              <NavLink
                to={item.to}
                title={collapsed ? item.label : undefined}
                className={({ isActive }): string =>
                  cn(
                    "flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors",
                    collapsed && "justify-center px-0",
                    isActive
                      ? "bg-primary text-primary-foreground"
                      : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
                  )
                }
                data-testid={`nav-${item.label.toLowerCase()}`}
              >
                <Icon className="size-4 shrink-0" aria-hidden />
                {!collapsed && <span className="truncate">{item.label}</span>}
              </NavLink>
            </li>
          );
        })}
      </ul>

      {/* User area + logout (folded in from the retired TopBar) */}
      <div
        className="flex flex-col gap-2 border-t border-border p-2"
        data-testid="user-area"
      >
        {user && !collapsed ? (
          <span
            className="truncate px-2 text-xs text-muted-foreground"
            data-testid="user-email"
          >
            {user.email}
          </span>
        ) : null}
        <Button
          variant="ghost"
          size={collapsed ? "icon" : "sm"}
          className={cn(!collapsed && "justify-start gap-3")}
          onClick={onLogout}
          aria-label="Logout"
          title={collapsed ? "Logout" : undefined}
          data-testid="logout-button"
        >
          <LogOut className="size-4 shrink-0" aria-hidden />
          {!collapsed && <span>Logout</span>}
        </Button>
      </div>
    </nav>
  );
}
