// ESLint 9 flat config.
//
// Rules of note:
//   - tailwindcss/no-arbitrary-value: error  → enforces the design-token layer
//     defined in tailwind.config.js. No raw `bg-[#abc]` / `mt-[13px]` literals
//     are allowed.
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
      "react-refresh/only-export-components": [
        "warn",
        { allowConstantExport: true },
      ],

      // Tailwind design-token enforcement
      "tailwindcss/no-arbitrary-value": "error",
      "tailwindcss/no-custom-classname": "warn",
      "tailwindcss/no-contradicting-classname": "error",

      // TypeScript
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
    },
  },
  // Test files: relax the unused-imports rule slightly.
  {
    files: ["**/*.test.{ts,tsx}", "src/test/**/*.{ts,tsx}"],
    rules: {
      "@typescript-eslint/no-explicit-any": "off",
    },
  },
  // Disable stylistic rules that conflict with Prettier.
  prettierConfig,
);
