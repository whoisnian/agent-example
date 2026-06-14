// ESLint 9 flat config.
//
// Rules of note:
//   - Color discipline (Tailwind v4, path-(b) guardrail): `eslint-plugin-tailwindcss`
//     is RETIRED because its only v4-capable releases are long-stalled pre-releases
//     (see migrate-design-system-base design D6). Its classname checks
//     (`no-custom-classname` / `no-contradicting-classname`) are intentionally GONE;
//     the contradiction check has no replacement here. Color discipline is enforced
//     plugin-independently via `no-restricted-syntax`: bare hex (`bg-[#abc]`) AND raw
//     color functions in arbitrary class values (`bg-[oklch(...)]`, `rgb(...)`,
//     `hsl(...)`) are banned in component code; variable-backed arbitraries
//     (`ring-[var(--color-ring)]`) stay allowed. A grep-based npm script
//     (`lint:colors`) backstops retired-palette class names + raw color literals.
//     Token DEFINITIONS in `globals.css` are the one allowed home for raw oklch().
//   - @typescript-eslint/recommended         → strict TS-aware base.

import js from "@eslint/js";
import tseslint from "typescript-eslint";
import reactPlugin from "eslint-plugin-react";
import reactHooksPlugin from "eslint-plugin-react-hooks";
import reactRefreshPlugin from "eslint-plugin-react-refresh";
import prettierConfig from "eslint-config-prettier";
import globals from "globals";

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
    },
    settings: {
      react: { version: "detect" },
    },
    rules: {
      // React / Hooks
      ...reactPlugin.configs.recommended.rules,
      ...reactPlugin.configs["jsx-runtime"].rules,
      ...reactHooksPlugin.configs.recommended.rules,
      // TypeScript provides prop typing; the runtime prop-types check is
      // redundant and misfires on forwardRef-typed shadcn primitives.
      "react/prop-types": "off",
      "react-refresh/only-export-components": ["warn", { allowConstantExport: true }],

      // Color-token discipline (plugin-independent; see header). Arbitrary class
      // values are allowed only when variable-backed (e.g. var(--color-ring)).
      // Bare hex AND raw color functions in arbitraries are rejected.
      "no-restricted-syntax": [
        "error",
        {
          selector: "Literal[value=/\\[#[0-9a-fA-F]{3,8}\\]/]",
          message:
            "Raw hex color literals are not allowed. Use a theme token (e.g. bg-background, text-foreground) or a var(--color-*)-backed arbitrary.",
        },
        {
          selector: "TemplateElement[value.raw=/\\[#[0-9a-fA-F]{3,8}\\]/]",
          message:
            "Raw hex color literals are not allowed. Use a theme token (e.g. bg-background, text-foreground) or a var(--color-*)-backed arbitrary.",
        },
        {
          selector: "Literal[value=/\\[(oklch|oklab|rgba?|hsla?|lab|lch|color)\\(/]",
          message:
            "Raw color-function literals in class values are not allowed. Use a semantic token (bg-primary, text-foreground) or a var(--color-*)-backed arbitrary.",
        },
        {
          selector: "TemplateElement[value.raw=/\\[(oklch|oklab|rgba?|hsla?|lab|lch|color)\\(/]",
          message:
            "Raw color-function literals in class values are not allowed. Use a semantic token (bg-primary, text-foreground) or a var(--color-*)-backed arbitrary.",
        },
      ],

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
  // Vendored shadcn primitives intentionally export a component plus its cva
  // variants from one file (e.g. Button + buttonVariants); the Fast-Refresh
  // "only export components" rule does not apply to this convention.
  {
    files: ["src/components/ui/**/*.{ts,tsx}"],
    rules: {
      "react-refresh/only-export-components": "off",
    },
  },
  // Node-run build/lint scripts (ESM): provide Node globals.
  {
    files: ["scripts/**/*.{js,mjs,cjs}"],
    languageOptions: {
      ecmaVersion: "latest",
      sourceType: "module",
      globals: { ...globals.node },
    },
  },
  // Disable stylistic rules that conflict with Prettier.
  prettierConfig,
);
