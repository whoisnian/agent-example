import { defineConfig, loadEnv, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import path from "node:path";

const THEME_BOOT_PLACEHOLDER = "'sha256-THEME_BOOT_HASH'";

/** sha256 of the FIRST inline (no `src`) <script> body, as a CSP source token. */
function inlineScriptCspHash(html: string): string | null {
  const body = html.match(/<script>([\s\S]*?)<\/script>/)?.[1];
  if (body === undefined) return null;
  return `'sha256-${createHash("sha256").update(body, "utf8").digest("base64")}'`;
}

/**
 * Replace the THEME_BOOT_HASH placeholder in the CSP with the sha256 of the
 * inline theme-boot script, computed from the EMITTED html (transformIndexHtml
 * runs in dev and build), so the hash always matches the served bytes — no
 * manual hash, no drift (design D1/C2). `closeBundle` re-verifies against the
 * written `dist/index.html` and fails the build on any mismatch.
 */
function themeCspHash(): Plugin {
  let outFile = "";
  return {
    name: "theme-csp-hash",
    transformIndexHtml: {
      order: "post",
      handler(html) {
        const hash = inlineScriptCspHash(html);
        return hash ? html.replace(THEME_BOOT_PLACEHOLDER, hash) : html;
      },
    },
    configResolved(cfg) {
      outFile = path.resolve(cfg.root, cfg.build.outDir, "index.html");
    },
    closeBundle() {
      let built: string;
      try {
        built = readFileSync(outFile, "utf8");
      } catch {
        return; // no index.html emitted (e.g. non-app build) — nothing to verify
      }
      const hash = inlineScriptCspHash(built);
      if (!hash) throw new Error("theme-csp-hash: no inline boot script in built index.html");
      if (built.includes(THEME_BOOT_PLACEHOLDER) || !built.includes(hash)) {
        throw new Error(
          "theme-csp-hash: CSP script-src hash does not match the emitted boot script " +
            `(expected ${hash} in dist/index.html). The boot script bytes and the CSP hash diverged.`,
        );
      }
    },
  };
}

// https://vitejs.dev/config/
export default defineConfig(({ mode }) => {
  // Backend origin for the dev proxy. Defaults to the Go API on :8080.
  // Override with VITE_DEV_PROXY_TARGET when the API runs elsewhere.
  const env = loadEnv(mode, process.cwd(), "");
  const proxyTarget = env.VITE_DEV_PROXY_TARGET || "http://localhost:8080";

  return {
    plugins: [react(), tailwindcss(), themeCspHash()],
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
