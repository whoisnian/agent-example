#!/usr/bin/env node
/*
 * Color-discipline backstop (Tailwind v4, path-(b) guardrail).
 *
 * `eslint-plugin-tailwindcss` was retired (no stable v4 support; see
 * migrate-design-system-base design D6). ESLint's `no-restricted-syntax` still
 * bans raw color literals in component class values; this script backstops the
 * parts ESLint does not cover:
 *   1. retired-palette class names (the pre-shadcn detached palette) in any
 *      source / index.html / globals.css, and
 *   2. raw hex / color-function literals in CSS OUTSIDE the token-definition
 *      blocks (`:root`, `.dark`, `@theme`) of globals.css — those blocks are the
 *      one allowed home for raw oklch() values.
 *
 * Exits non-zero (with a report) on any violation. Wired as `npm run lint:colors`.
 */
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, extname } from "node:path";

const ROOT = new URL("..", import.meta.url).pathname;
const SRC = join(ROOT, "src");
const GLOBALS = join(SRC, "styles", "globals.css");
const INDEX_HTML = join(ROOT, "index.html");

/** Retired pre-shadcn palette classes (current valid tokens are NOT matched). */
const RETIRED_CLASS = /\b(?:bg-bg|bg-surface|text-text(?:-muted)?|(?:bg|text|border)-danger)\b/;
/** Raw color literals: hex arbitrary or a color-function. */
const RAW_HEX = /\[#[0-9a-fA-F]{3,8}\]/;
const RAW_COLORFN = /\b(?:oklch|oklab|rgba?|hsla?|lab|lch|color)\(/;

const violations = [];

function walk(dir) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    const st = statSync(p);
    if (st.isDirectory()) {
      walk(p);
    } else if ([".ts", ".tsx"].includes(extname(p))) {
      scanRetired(p);
    }
  }
}

function scanRetired(file) {
  const lines = readFileSync(file, "utf8").split("\n");
  lines.forEach((line, i) => {
    if (RETIRED_CLASS.test(line)) {
      violations.push(`${file}:${i + 1}  retired palette class → ${line.trim()}`);
    }
  });
}

walk(SRC);

// index.html: retired classes + raw hex (no token definitions live here).
readFileSync(INDEX_HTML, "utf8")
  .split("\n")
  .forEach((line, i) => {
    if (RETIRED_CLASS.test(line) || RAW_HEX.test(line)) {
      violations.push(`${INDEX_HTML}:${i + 1}  raw color / retired class → ${line.trim()}`);
    }
  });

// globals.css: raw hex anywhere is banned; raw color-functions are allowed ONLY
// inside token-definition blocks (:root / .dark / @theme).
{
  const lines = readFileSync(GLOBALS, "utf8").split("\n");
  let depth = 0;
  let inTokenBlock = false;
  let blockStartDepth = 0;
  lines.forEach((line, i) => {
    const opensTokenBlock = /(^|\s)(:root|\.dark|\.light|@theme)\b/.test(line);
    if (opensTokenBlock && line.includes("{")) {
      inTokenBlock = true;
      blockStartDepth = depth;
    }
    const stripped = line.replace(/\/\*.*?\*\//g, ""); // ignore inline comments
    if (RAW_HEX.test(stripped)) {
      violations.push(`${GLOBALS}:${i + 1}  raw hex literal → ${line.trim()}`);
    }
    if (!inTokenBlock && RAW_COLORFN.test(stripped) && !stripped.trim().startsWith("*")) {
      violations.push(
        `${GLOBALS}:${i + 1}  raw color-function outside token block → ${line.trim()}`,
      );
    }
    for (const ch of line) {
      if (ch === "{") depth++;
      else if (ch === "}") {
        depth--;
        if (inTokenBlock && depth <= blockStartDepth) inTokenBlock = false;
      }
    }
  });
}

if (violations.length > 0) {
  console.error("✗ color-discipline check failed:\n");
  for (const v of violations) console.error("  " + v);
  console.error(`\n${violations.length} violation(s). Use semantic tokens or var(--color-*).`);
  process.exit(1);
}
console.log("✓ color-discipline check passed (no retired palette classes / raw color literals)");
