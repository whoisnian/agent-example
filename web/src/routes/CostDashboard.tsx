import type { JSX } from "react";
import { useMemo, useState } from "react";
import { TokenBar } from "@/components/costs/TokenBar";
import { useMyCostQuery } from "@/features/costs/queries";
import type { MyCostParams } from "@/features/costs/api";
import type { CostGroupBy, CostGroupItem, CostRollup } from "@/features/costs/types";
import { barFraction, formatAmount, sumAmounts } from "@/features/costs/format";

/** UI grouping choice. `none` is the lifetime Total (no grouping, no window). */
type GroupChoice = "none" | CostGroupBy;
type WindowDays = 7 | 30 | 90;

const GROUP_OPTIONS: ReadonlyArray<{ value: GroupChoice; label: string }> = [
  { value: "none", label: "Total" },
  { value: "day", label: "By day" },
  { value: "task_type", label: "By task type" },
  { value: "model", label: "By model" },
];

const WINDOW_OPTIONS: readonly WindowDays[] = [7, 30, 90];

/** Window for a preset: `to = now`, `from = to − Nd`, both RFC3339. Every preset
 *  stays within the backend's 366d grouped cap. */
function windowFor(days: WindowDays): { from: string; to: string } {
  const to = new Date();
  const from = new Date(to.getTime() - days * 24 * 60 * 60 * 1000);
  return { from: from.toISOString(), to: to.toISOString() };
}

function GroupedRows({ items }: { items: CostGroupItem[] }): JSX.Element {
  if (items.length === 0) {
    return (
      <p data-testid="cost-empty" className="text-sm text-text-muted">
        No spend in this window.
      </p>
    );
  }
  // Bars scale to the largest item; server order is preserved (no client re-sort).
  const max = items.reduce(
    (m, it) => (Number(it.totals.amount_usd) > Number(m) ? it.totals.amount_usd : m),
    "0",
  );
  const total = sumAmounts(items.map((it) => it.totals.amount_usd));
  return (
    <div className="flex flex-col gap-3">
      <div data-testid="cost-group-total" className="text-sm text-text-muted">
        Window total{" "}
        <span className="font-mono text-text" title={`${total} USD`}>
          {formatAmount(total)}
        </span>
      </div>
      <ul data-testid="cost-group-list" className="flex flex-col gap-1">
        {items.map((it) => (
          <li
            key={it.key}
            data-testid="cost-group-row"
            data-key={it.key}
            className="flex items-center gap-3 text-sm"
          >
            <span className="w-48 shrink-0 truncate font-mono text-text" title={it.key}>
              {it.key}
            </span>
            <span className="h-2 flex-1 overflow-hidden rounded bg-surface">
              <span
                className="block h-full bg-accent"
                style={{ width: `${barFraction(it.totals.amount_usd, max) * 100}%` }}
              />
            </span>
            <span
              className="w-24 shrink-0 text-right font-mono text-text-muted"
              title={`${it.totals.amount_usd} USD`}
            >
              {formatAmount(it.totals.amount_usd)}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** Exhaustive render over the discriminated rollup. */
function RollupView({ data }: { data: CostRollup }): JSX.Element {
  if ("group_by" in data) {
    return <GroupedRows items={data.items} />;
  }
  if ("total" in data) {
    const isZero = data.total.amount_usd === "0.00000000";
    return (
      <div className="flex flex-col gap-2">
        <TokenBar cost={data.total} />
        {isZero ? (
          <p data-testid="cost-empty" className="text-sm text-text-muted">
            No spend yet.
          </p>
        ) : null}
      </div>
    );
  }
  // Neither branch matched — the server emitted an impossible shape.
  const exhaustive: never = data;
  return exhaustive;
}

export function CostDashboard(): JSX.Element {
  const [group, setGroup] = useState<GroupChoice>("none");
  const [windowDays, setWindowDays] = useState<WindowDays>(30);

  // Stable per (group, window) so `to = now()` is captured once per selection
  // rather than on every render (which would thrash the query key).
  const params = useMemo<MyCostParams>(() => {
    if (group === "none") return {};
    return { groupBy: group, ...windowFor(windowDays) };
  }, [group, windowDays]);

  const query = useMyCostQuery(params);

  return (
    <section data-testid="cost-dashboard-page">
      <h1 className="mb-4 text-2xl font-semibold text-text">Cost</h1>

      <div className="mb-6 flex flex-wrap items-center gap-4">
        <label className="flex items-center gap-2 text-sm text-text-muted">
          Group
          <select
            data-testid="cost-group-select"
            value={group}
            onChange={(e) => setGroup(e.target.value as GroupChoice)}
            className="rounded border border-border bg-surface px-2 py-1 text-text"
          >
            {GROUP_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </label>

        {group !== "none" ? (
          <label className="flex items-center gap-2 text-sm text-text-muted">
            Window
            <select
              data-testid="cost-window-select"
              value={windowDays}
              onChange={(e) => setWindowDays(Number(e.target.value) as WindowDays)}
              className="rounded border border-border bg-surface px-2 py-1 text-text"
            >
              {WINDOW_OPTIONS.map((d) => (
                <option key={d} value={d}>
                  {d}d
                </option>
              ))}
            </select>
          </label>
        ) : null}
      </div>

      {query.isPending ? (
        <p data-testid="cost-loading" className="text-sm text-text-muted">
          Loading…
        </p>
      ) : query.error ? (
        <p data-testid="cost-error" className="text-sm text-danger">
          Failed to load cost. Please retry.
        </p>
      ) : (
        <RollupView data={query.data} />
      )}
    </section>
  );
}
