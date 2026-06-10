import type { JSX } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import {
  Bot,
  ChevronsUpDown,
  DollarSign,
  ListTodo,
  LogOut,
  Plus,
  Settings,
  type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/cn";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { RecentTasks } from "@/components/layout/RecentTasks";
import { useAuthStore } from "@/features/auth/store";

interface MenuItem {
  to: string;
  label: string;
  icon: LucideIcon;
}

const menuItems: readonly MenuItem[] = [
  { to: "/tasks", label: "Tasks", icon: ListTodo },
  { to: "/cost", label: "Cost", icon: DollarSign },
  { to: "/settings", label: "Settings", icon: Settings },
] as const;

/**
 * Left navigation column of the three-column shell, fixed width (the collapse
 * toggle is retired). Top to bottom: brand row, the "New task" primary action,
 * the Recents task list, and the avatar-style user area that triggers the
 * primary-navigation popup menu (Tasks / Cost / Settings / Logout).
 */
export function SideNav(): JSX.Element {
  const navigate = useNavigate();
  const location = useLocation();
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);

  const onLogout = (): void => {
    logout();
    navigate("/login", { replace: true });
  };

  return (
    <nav
      className="flex w-56 shrink-0 flex-col border-r border-border bg-card"
      aria-label="Primary"
      data-testid="side-nav"
    >
      {/* Brand row */}
      <div className="flex h-14 items-center gap-2 px-3">
        <div className="flex size-8 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground">
          <Bot className="size-5" aria-hidden />
        </div>
        <span className="flex-1 truncate text-sm font-semibold text-foreground">
          Agent Task Platform
        </span>
      </div>

      {/* New task primary action */}
      <div className="flex px-2 pt-1">
        <Button asChild size="sm" className="w-full justify-start gap-3">
          <Link to="/tasks/new" aria-label="New task" data-testid="nav-new-task">
            <Plus className="size-4 shrink-0" aria-hidden />
            <span>New task</span>
          </Link>
        </Button>
      </div>

      {/* Recents takes the remaining height. */}
      <RecentTasks />

      {/* Avatar-style user area = trigger of the primary-navigation menu. */}
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            className="flex items-center gap-2 border-t border-border p-2 text-left transition-colors hover:bg-accent/50"
            data-testid="user-area"
            aria-label="Open navigation menu"
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
            {user ? (
              <span
                className="min-w-0 flex-1 truncate text-xs text-muted-foreground"
                data-testid="user-email"
              >
                {user.email}
              </span>
            ) : (
              <span className="min-w-0 flex-1" />
            )}
            <ChevronsUpDown className="size-4 shrink-0 text-muted-foreground" aria-hidden />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          side="top"
          align="start"
          className="w-52"
          data-testid="user-menu"
        >
          {menuItems.map((item) => {
            const Icon = item.icon;
            const active = location.pathname.startsWith(item.to);
            return (
              <DropdownMenuItem
                key={item.to}
                data-testid={`nav-${item.label.toLowerCase()}`}
                className={cn(active && "bg-accent text-accent-foreground")}
                onSelect={() => navigate(item.to)}
              >
                <Icon aria-hidden />
                <span>{item.label}</span>
              </DropdownMenuItem>
            );
          })}
          <DropdownMenuSeparator />
          <DropdownMenuItem data-testid="logout-button" onSelect={onLogout}>
            <LogOut aria-hidden />
            <span>Logout</span>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </nav>
  );
}
