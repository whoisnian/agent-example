import { createBrowserRouter, Navigate, type RouteObject } from "react-router-dom";
import { RootLayout } from "@/routes/root-layout";
import { RequireAuth } from "@/routes/require-auth";
import { TaskList } from "@/routes/TaskList";
import { TaskCreate } from "@/routes/TaskCreate";
import { TaskDetail } from "@/routes/TaskDetail";
import { CostDashboard } from "@/routes/CostDashboard";
import { SettingsPlaceholder } from "@/routes/placeholders/SettingsPlaceholder";
import { LoginPage } from "@/routes/LoginPage";
import { NotFoundPlaceholder } from "@/routes/placeholders/NotFoundPlaceholder";

export const routes: RouteObject[] = [
  { path: "/login", element: <LoginPage /> },
  {
    path: "/",
    element: (
      <RequireAuth>
        <RootLayout />
      </RequireAuth>
    ),
    children: [
      { index: true, element: <Navigate to="/tasks/new" replace /> },
      { path: "tasks", element: <TaskList /> },
      { path: "tasks/new", element: <TaskCreate /> },
      { path: "tasks/:id", element: <TaskDetail /> },
      { path: "cost", element: <CostDashboard /> },
      { path: "settings", element: <SettingsPlaceholder /> },
    ],
  },
  { path: "*", element: <NotFoundPlaceholder /> },
];

export function createAppRouter(): ReturnType<typeof createBrowserRouter> {
  return createBrowserRouter(routes);
}
