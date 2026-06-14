import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

// https://vitejs.dev/config/
export default defineConfig(({ mode }) => {
  // Backend origin for the dev proxy. Defaults to the Go API on :8080.
  // Override with VITE_DEV_PROXY_TARGET when the API runs elsewhere.
  const env = loadEnv(mode, process.cwd(), "");
  const proxyTarget = env.VITE_DEV_PROXY_TARGET || "http://localhost:8080";

  return {
    plugins: [react(), tailwindcss()],
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
      //
      // changeOrigin stays FALSE on purpose: the API's WS gateway enforces a
      // same-origin check (coder/websocket compares the request Host against the
      // Origin header). Rewriting Host to the target (changeOrigin: true) makes
      // Host=localhost:8080 while the browser's Origin stays the dev host, so the
      // upgrade 403s on any non-localhost:8080 host (LAN IP, localhost:5173, …).
      // Forwarding the original Host keeps Host==Origin → same-origin passes on
      // every host with no per-host WS_ALLOWED_ORIGINS config. Gin routes by path,
      // so the REST proxy doesn't need the rewrite.
      proxy: {
        "/api": {
          target: proxyTarget,
          changeOrigin: false,
          ws: true,
        },
      },
    },
    preview: {
      port: 4173,
    },
  };
});
