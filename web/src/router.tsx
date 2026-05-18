import { createBrowserRouter, Navigate, type RouteObject } from "react-router-dom";
import { RootLayout } from "@/routes/root-layout";
import { RequireAuth } from "@/routes/require-auth";
import { TaskListPlaceholder } from "@/routes/placeholders/TaskListPlaceholder";
import { TaskDetailPlaceholder } from "@/routes/placeholders/TaskDetailPlaceholder";
import { CostDashboardPlaceholder } from "@/routes/placeholders/CostDashboardPlaceholder";
import { SettingsPlaceholder } from "@/routes/placeholders/SettingsPlaceholder";
import { LoginPlaceholder } from "@/routes/placeholders/LoginPlaceholder";
import { NotFoundPlaceholder } from "@/routes/placeholders/NotFoundPlaceholder";

export const routes: RouteObject[] = [
  { path: "/login", element: <LoginPlaceholder /> },
  {
    path: "/",
    element: (
      <RequireAuth>
        <RootLayout />
      </RequireAuth>
    ),
    children: [
      { index: true, element: <Navigate to="/tasks" replace /> },
      { path: "tasks", element: <TaskListPlaceholder /> },
      { path: "tasks/:id", element: <TaskDetailPlaceholder /> },
      { path: "cost", element: <CostDashboardPlaceholder /> },
      { path: "settings", element: <SettingsPlaceholder /> },
    ],
  },
  { path: "*", element: <NotFoundPlaceholder /> },
];

export function createAppRouter(): ReturnType<typeof createBrowserRouter> {
  return createBrowserRouter(routes);
}
