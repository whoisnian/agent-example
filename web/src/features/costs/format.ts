/**
 * Decimal-string-safe cost formatting helpers, shared by CostBadge, TokenBar,
 * and the CostDashboard.
 *
 * `amount_usd` is a NUMERIC(18,8) rendered to a decimal STRING by the API. We
 * never parse it to a float for display. `formatAmount` truncates (does NOT
 * round) to 4 fractional digits by pure string slicing; the full value should
 * be kept available elsewhere (e.g. a `title` attribute). `barFraction` is the
 * ONLY place a string amount is turned into a number, and that number is used
 * solely for bar geometry — never displayed.
 */

/** Truncate (not round) a decimal-string amount to 4 dp for display. */
export function formatAmount(amount: string): string {
  const [intPart, fracPart = ""] = amount.split(".");
  const frac = fracPart.slice(0, 4).padEnd(4, "0");
  return `$${intPart}.${frac}`;
}

/**
 * Fraction in [0, 1] of `amount` relative to `max`, for bar width only. Returns
 * 0 when `max` is zero/empty or either value is non-finite (so an all-zero
 * grouped window renders zero-width bars, never `NaN`).
 */
export function barFraction(amount: string, max: string): number {
  const a = Number(amount);
  const m = Number(max);
  if (!Number.isFinite(a) || !Number.isFinite(m) || m <= 0) return 0;
  const f = a / m;
  if (f <= 0) return 0;
  return f >= 1 ? 1 : f;
}

/**
 * Exact sum of scale-8 decimal-string amounts, returned as a scale-8 decimal
 * string. Uses BigInt on the fixed-scale integer representation so there is no
 * float rounding — this is the grouped-view total, which the backend guarantees
 * reconciles with the per-item sum. Defensive against varying fractional length.
 */
export function sumAmounts(amounts: string[]): string {
  let total = 0n;
  for (const raw of amounts) {
    const neg = raw.trimStart().startsWith("-");
    const [intPart = "0", fracPart = ""] = raw.replace("-", "").trim().split(".");
    const digits = `${intPart}${fracPart.slice(0, 8).padEnd(8, "0")}`;
    const scaled = /^\d+$/.test(digits) ? BigInt(digits) : 0n;
    total += neg ? -scaled : scaled;
  }
  const neg = total < 0n;
  const abs = (neg ? -total : total).toString().padStart(9, "0");
  return `${neg ? "-" : ""}${abs.slice(0, -8)}.${abs.slice(-8)}`;
}
