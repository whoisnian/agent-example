import { describe, expect, it } from "vitest";
import { barFraction, formatAmount, sumAmounts } from "./format";

describe("formatAmount", () => {
  it("truncates (does not round) to 4 dp from the decimal string", () => {
    expect(formatAmount("0.06759999")).toBe("$0.0675");
    expect(formatAmount("1.72000000")).toBe("$1.7200");
    expect(formatAmount("0.00000000")).toBe("$0.0000");
  });

  it("pads short fractions and keeps the integer part", () => {
    expect(formatAmount("12.5")).toBe("$12.5000");
    expect(formatAmount("7")).toBe("$7.0000");
  });
});

describe("barFraction", () => {
  it("returns the clamped [0,1] fraction of amount over max", () => {
    expect(barFraction("0.50000000", "1.00000000")).toBeCloseTo(0.5);
    expect(barFraction("2.00000000", "1.00000000")).toBe(1);
  });

  it("returns 0 when max is zero (no NaN) or inputs are non-finite", () => {
    expect(barFraction("0.00000000", "0.00000000")).toBe(0);
    expect(barFraction("1.00000000", "0")).toBe(0);
    expect(barFraction("nope", "1.00000000")).toBe(0);
  });
});

describe("sumAmounts", () => {
  it("sums scale-8 decimal strings exactly without float drift", () => {
    expect(sumAmounts(["0.10000000", "0.32000000"])).toBe("0.42000000");
    expect(sumAmounts(["0.30000000", "0.12000000"])).toBe("0.42000000");
    // A classic float pitfall (0.1 + 0.2) stays exact here.
    expect(sumAmounts(["0.10000000", "0.20000000"])).toBe("0.30000000");
  });

  it("handles an empty list and a single item", () => {
    expect(sumAmounts([])).toBe("0.00000000");
    expect(sumAmounts(["1.72000000"])).toBe("1.72000000");
  });
});
