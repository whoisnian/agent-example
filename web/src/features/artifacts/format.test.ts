import { describe, expect, it } from "vitest";
import { formatBytes } from "./format";

describe("formatBytes", () => {
  it("renders sub-1KB sizes as whole bytes", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(1)).toBe("1 B");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(999)).toBe("999 B");
  });

  it("renders exactly-on-the-boundary sizes with one decimal place", () => {
    expect(formatBytes(1000)).toBe("1.0 KB");
    expect(formatBytes(1_000_000)).toBe("1.0 MB");
    expect(formatBytes(1_000_000_000)).toBe("1.0 GB");
    expect(formatBytes(1_000_000_000_000)).toBe("1.0 TB");
  });

  it("renders KB/MB/GB with one decimal place", () => {
    expect(formatBytes(12_288)).toBe("12.3 KB");
    expect(formatBytes(1_200_000)).toBe("1.2 MB");
    expect(formatBytes(2_500_000_000)).toBe("2.5 GB");
  });

  it("is defensive against negative / non-finite input", () => {
    expect(formatBytes(-5)).toBe("0 B");
    expect(formatBytes(Number.NaN)).toBe("0 B");
    expect(formatBytes(Number.POSITIVE_INFINITY)).toBe("0 B");
  });
});
