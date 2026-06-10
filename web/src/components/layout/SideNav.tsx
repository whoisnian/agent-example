import type { JSX } from "react";
import { Link, NavLink, useNavigate } from "react-router-dom";
import {
  Bot,
  DollarSign,
  ListTodo,
  LogOut,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  Settings,
  type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/cn";
import { Button } from "@/components/ui/button";
import { RecentTasks } from "@/components/layout/RecentTasks";
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
 * Left navigation column of the three-column shell. Top to bottom: brand row,
 * the "New task" primary action, primary nav, the Recents task list (hidden on
 * the icon rail), and the avatar-style user area + logout (folded in from the
 * retired TopBar). Collapses to an icon rail via the UI store.
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
        collapsed ? "w-16" : "w-56",
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

      {/* New task primary action */}
      <div className={cn("flex px-2 pt-1", collapsed && "justify-center")}>
        <Button
          asChild
          size={collapsed ? "icon" : "sm"}
          className={cn(collapsed ? "size-8" : "w-full justify-start gap-3")}
          title={collapsed ? "New task" : undefined}
        >
          <Link to="/tasks/new" aria-label="New task" data-testid="nav-new-task">
            <Plus className="size-4 shrink-0" aria-hidden />
            {!collapsed && <span>New task</span>}
          </Link>
        </Button>
      </div>

      {/* Primary nav */}
      <ul className="flex flex-col gap-1 p-2">
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

      {/* Recents (expanded only) takes the remaining height; a spacer keeps
          the user area pinned to the bottom on the icon rail. */}
      {collapsed ? <div className="flex-1" /> : <RecentTasks />}

      {/* Avatar-style user area + logout (folded in from the retired TopBar) */}
      <div
        className={cn(
          "flex gap-2 border-t border-border p-2",
          collapsed ? "flex-col items-center" : "items-center",
        )}
        data-testid="user-area"
      >
        {user ? (
          <div
            className="flex size-8 shrink-0 items-center justify-center rounded-full bg-primary text-sm font-medium uppercase text-primary-foreground"
            title={user.email}
            data-testid="user-avatar"
          >
            {user.email.charAt(0)}
          </div>
        ) : null}
        {user && !collapsed ? (
          <span
            className="min-w-0 flex-1 truncate text-xs text-muted-foreground"
            data-testid="user-email"
          >
            {user.email}
          </span>
        ) : null}
        <Button
          variant="ghost"
          size="icon"
          className="size-8 shrink-0"
          onClick={onLogout}
          aria-label="Logout"
          title="Logout"
          data-testid="logout-button"
        >
          <LogOut className="size-4" aria-hidden />
        </Button>
      </div>
    </nav>
  );
}
