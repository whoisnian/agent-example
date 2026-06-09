import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

/**
 * Merge Tailwind class names: `clsx` resolves conditionals/arrays, then
 * `tailwind-merge` de-dupes conflicting Tailwind utilities (last wins).
 * Registered as a lint `callees` entry so class ordering rules still apply.
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
