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
    // are intentionally passed as separate args (not one literal) so the
    // tailwindcss/no-contradicting-classname lint does not flag the assertion.
    const base = "px-2 py-1";
    const override = "px-4";
    expect(cn(base, override)).toBe("py-1 px-4");
  });

  it("preserves variable-backed arbitrary values", () => {
    const cls = "ring-[hsl(var(--ring))]";
    expect(cn(cls)).toBe("ring-[hsl(var(--ring))]");
  });
});
