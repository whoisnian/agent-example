/**
 * Artifact display helpers.
 *
 * `bytes` is a plain integer file size (int8), well within Number.MAX_SAFE_INTEGER
 * for any MVP artifact, so we convert it to a `number` and format it directly.
 * This is DISTINCT from the decimal-string money rule (features/costs/format.ts),
 * which exists only to avoid float rounding of `amount_usd` — file sizes have no
 * such constraint.
 */

const UNITS = ["B", "KB", "MB", "GB", "TB"] as const;

/**
 * Human-readable size from a byte count, decimal (1000) base. Sizes below 1 KB
 * render as whole bytes (e.g. `"512 B"`); 1 KB and up render with one decimal
 * place (e.g. `"1.0 KB"`, `"1.2 MB"`). Defensive against negative / non-finite
 * input (returns `"0 B"`).
 */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  if (bytes < 1000) return `${Math.floor(bytes)} B`;
  let value = bytes;
  let unit = 0;
  while (value >= 1000 && unit < UNITS.length - 1) {
    value /= 1000;
    unit += 1;
  }
  return `${value.toFixed(1)} ${UNITS[unit]}`;
}
