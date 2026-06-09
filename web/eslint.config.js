// ESLint 9 flat config.
//
// Rules of note:
//   - Color discipline: with the shadcn CSS-variable theme, arbitrary values are
//     permitted ONLY when they reference a theme variable (e.g.
//     `ring-[hsl(var(--ring))]`); bare hex literals (`bg-[#abc]`) stay banned via
//     a `no-restricted-syntax` guard. `tailwindcss/no-arbitrary-value` is off
//     because vendored shadcn primitives need a few variable-backed arbitraries.
//   - tailwindcss/no-custom-classname: warn → catches typos.
//   - @typescript-eslint/recommended         → strict TS-aware base.
//
// Tailwind plugin resolution under npm flat node_modules is direct; no path
// override needed (unlike the pnpm symlinked layout we had previously).

import path from "node:path";
import { fileURLToPath } from "node:url";

import js from "@eslint/js";
import tseslint from "typescript-eslint";
import reactPlugin from "eslint-plugin-react";
import reactHooksPlugin from "eslint-plugin-react-hooks";
import reactRefreshPlugin from "eslint-plugin-react-refresh";
import tailwindPlugin from "eslint-plugin-tailwindcss";
import prettierConfig from "eslint-config-prettier";
import globals from "globals";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

export default tseslint.config(
  {
    ignores: ["dist/**", "node_modules/**", "coverage/**"],
  },
  js.configs.recommended,
  tseslint.configs.recommended,
  {
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: "latest",
      sourceType: "module",
      parserOptions: {
        ecmaFeatures: { jsx: true },
      },
      globals: {
        ...globals.browser,
        ...globals.node,
      },
    },
    plugins: {
      react: reactPlugin,
      "react-hooks": reactHooksPlugin,
      "react-refresh": reactRefreshPlugin,
      tailwindcss: tailwindPlugin,
    },
    settings: {
      react: { version: "detect" },
      tailwindcss: {
        callees: ["clsx", "cn", "classNames"],
        config: path.resolve(__dirname, "tailwind.config.js"),
      },
    },
    rules: {
      // React / Hooks
      ...reactPlugin.configs.recommended.rules,
      ...reactPlugin.configs["jsx-runtime"].rules,
      ...reactHooksPlugin.configs.recommended.rules,
      // TypeScript provides prop typing; the runtime prop-types check is
      // redundant and misfires on forwardRef-typed shadcn primitives.
      "react/prop-types": "off",
      "react-refresh/only-export-components": [
        "warn",
        { allowConstantExport: true },
      ],

      // Tailwind design-token enforcement. Arbitrary values are allowed only
      // when variable-backed; bare hex color literals are rejected below.
      "tailwindcss/no-custom-classname": "warn",
      "tailwindcss/no-contradicting-classname": "error",
      "no-restricted-syntax": [
        "error",
        {
          selector: "Literal[value=/\\[#[0-9a-fA-F]{3,8}\\]/]",
          message:
            "Raw hex color literals are not allowed. Use a theme token (e.g. bg-background, text-foreground) or hsl(var(--token)).",
        },
        {
          selector: "TemplateElement[value.raw=/\\[#[0-9a-fA-F]{3,8}\\]/]",
          message:
            "Raw hex color literals are not allowed. Use a theme token (e.g. bg-background, text-foreground) or hsl(var(--token)).",
        },
      ],

      // TypeScript
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
    },
  },
  // Test files: relax the unused-imports rule slightly, and don't treat
  // arbitrary class-string fixtures passed to cn() as Tailwind classnames.
  {
    files: ["**/*.test.{ts,tsx}", "src/test/**/*.{ts,tsx}"],
    rules: {
      "@typescript-eslint/no-explicit-any": "off",
      "tailwindcss/no-custom-classname": "off",
    },
  },
  // Vendored shadcn primitives intentionally export a component plus its cva
  // variants from one file (e.g. Button + buttonVariants); the Fast-Refresh
  // "only export components" rule does not apply to this convention.
  {
    files: ["src/components/ui/**/*.{ts,tsx}"],
    rules: {
      "react-refresh/only-export-components": "off",
    },
  },
  // Disable stylistic rules that conflict with Prettier.
  prettierConfig,
);
