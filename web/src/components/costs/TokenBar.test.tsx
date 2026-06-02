import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { TokenBar } from "./TokenBar";
import type { CostSummary } from "@/features/tasks/types";

function cost(over: Partial<CostSummary> = {}): CostSummary {
  return {
    amount_usd: "0.00000000",
    input_tokens: 0,
    output_tokens: 0,
    cached_tokens: 0,
    tool_calls: 0,
    wall_time_ms: 0,
    ...over,
  };
}

describe("TokenBar", () => {
  it("shows the amount truncated from the decimal string (not rounded from a float)", () => {
    render(<TokenBar cost={cost({ amount_usd: "0.06759999" })} />);
    const amt = screen.getByTestId("token-bar-amount");
    expect(amt).toHaveTextContent("$0.0675");
    // Full precision preserved in the title.
    expect(amt).toHaveAttribute("title", "0.06759999 USD");
  });

  it("renders all token / tool / wall fields", () => {
    render(
      <TokenBar
        cost={cost({
          amount_usd: "1.72000000",
          input_tokens: 1200,
          output_tokens: 340,
          cached_tokens: 80,
          tool_calls: 3,
          wall_time_ms: 4500,
        })}
      />,
    );
    const bar = screen.getByTestId("token-bar");
    expect(bar).toHaveTextContent("1,200");
    expect(bar).toHaveTextContent("340");
    expect(bar).toHaveTextContent("80");
    expect(bar).toHaveTextContent("3");
    expect(bar).toHaveTextContent("4,500ms");
  });

  it("renders an all-zero cost without error", () => {
    render(<TokenBar cost={cost()} />);
    expect(screen.getByTestId("token-bar-amount")).toHaveTextContent("$0.0000");
  });
});
