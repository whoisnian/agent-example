import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

// https://vitejs.dev/config/
export default defineConfig(({ mode }) => {
  // Backend origin for the dev proxy. Defaults to the Go API on :8080.
  // Override with VITE_DEV_PROXY_TARGET when the API runs elsewhere.
  const env = loadEnv(mode, process.cwd(), "");
  const proxyTarget = env.VITE_DEV_PROXY_TARGET || "http://localhost:8080";

  return {
    plugins: [react()],
    resolve: {
      alias: {
        "@": path.resolve(__dirname, "./src"),
      },
    },
    server: {
      port: 5173,
      // Same-origin API calls (VITE_API_BASE_URL empty) hit :5173, which has no
      // /api routes → 404 (e.g. POST /api/v1/auth/login). Proxy /api to the
      // backend; `ws: true` also forwards the /api/v1/ws WebSocket upgrade.
      proxy: {
        "/api": {
          target: proxyTarget,
          changeOrigin: true,
          ws: true,
        },
      },
    },
    preview: {
      port: 4173,
    },
  };
});
