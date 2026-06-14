import { describe, expect, it } from "vitest";
import { cn } from "./cn";

describe("cn", () => {
  it("joins truthy class names and drops falsy ones", () => {
    const off = false as boolean;
    const missing = null as string | null;
    expect(cn("a", off && "b", missing, undefined, "c")).toBe("a c");
  });

  it("resolves conditional and array inputs via clsx", () => {
    expect(cn(["a", "b"], { c: true, d: false })).toBe("a b c");
  });

  it("de-dupes conflicting Tailwind utilities with last-wins", () => {
    // tailwind-merge keeps the later padding utility. The two padding classes
    // are intentionally passed as separate args (not one literal) — a stylistic
    // habit retained from when the (now-retired) tailwindcss/no-contradicting-
    // classname lint would have flagged a single contradicting literal.
    const base = "px-2 py-1";
    const override = "px-4";
    expect(cn(base, override)).toBe("py-1 px-4");
  });

  it("preserves variable-backed arbitrary values", () => {
    // Tailwind v4 token form: the variable is a complete color, referenced via
    // var(--color-ring) (no hsl() wrapper). tailwind-merge must keep it intact.
    const cls = "ring-[var(--color-ring)]";
    expect(cn(cls)).toBe("ring-[var(--color-ring)]");
  });
});
