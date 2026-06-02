import { describe, expect, it } from "vitest";
import { controlAvailability, TASK_STATUSES } from "./types";

describe("controlAvailability", () => {
  it.each([
    ["pending", { canPause: true, canResume: false, canCancel: true }],
    ["running", { canPause: true, canResume: false, canCancel: true }],
    ["paused", { canPause: false, canResume: true, canCancel: true }],
    ["cancelled", { canPause: false, canResume: false, canCancel: false }],
    ["succeeded", { canPause: false, canResume: false, canCancel: false }],
    ["failed", { canPause: false, canResume: false, canCancel: false }],
  ] as const)("derives availability for %s", (status, expected) => {
    expect(controlAvailability(status)).toEqual(expected);
  });

  it("covers every TASK_STATUSES member", () => {
    for (const status of TASK_STATUSES) {
      expect(controlAvailability(status)).toBeDefined();
    }
  });

  it("returns all-false for an unknown / version-only status", () => {
    // queued / cancelling are version-only and never reach task.status; a typo
    // must not enable an action.
    for (const status of ["queued", "cancelling", "bogus", ""]) {
      expect(controlAvailability(status)).toEqual({
        canPause: false,
        canResume: false,
        canCancel: false,
      });
    }
  });
});
