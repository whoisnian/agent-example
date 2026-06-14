import type { JSX, FormEvent, KeyboardEvent } from "react";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { ArrowUp, Settings2 } from "lucide-react";
import { cn } from "@/lib/cn";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { ApiError } from "@/services/http";
import { useCreateTaskMutation } from "@/features/tasks/mutations";
import type { CreateTaskRequest, InvalidInputData } from "@/features/tasks/types";

// task_type values mirror the worker AgentRegistry (code-gen / research).
// Not API-enforced; see design D8.
const TASK_TYPES = ["code-gen", "research"] as const;

function isInvalidInputData(x: unknown): x is InvalidInputData {
  return (
    typeof x === "object" &&
    x !== null &&
    typeof (x as Record<string, unknown>)["field"] === "string"
  );
}

/**
 * Chat-style creation entry: a centered greeting, the prompt composer card
 * with task-type chips, and an "Advanced" disclosure for params / lane. There
 * is deliberately no title input — the API derives the title from the prompt
 * (spec task-write-api). Visual language matches the TaskDetail conversation.
 */
export function TaskCreate(): JSX.Element {
  const navigate = useNavigate();
  const mutation = useCreateTaskMutation();

  const [taskType, setTaskType] = useState<string>(TASK_TYPES[0]);
  const [prompt, setPrompt] = useState("");
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [paramsText, setParamsText] = useState("");
  const [lane, setLane] = useState("");

  // field name -> message; key "" / "body" is a form-level error.
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const submit = (): void => {
    setFieldErrors({});

    let params: unknown;
    if (paramsText.trim() !== "") {
      try {
        params = JSON.parse(paramsText);
      } catch {
        setFieldErrors({ params: "params must be valid JSON" });
        setAdvancedOpen(true);
        return;
      }
    }

    // No title — the server derives it from the prompt.
    const body: CreateTaskRequest = { task_type: taskType, prompt };
    if (params !== undefined) body.params = params;
    if (lane.trim() !== "") body.lane = lane.trim();

    mutation.mutate(body, {
      onSuccess: (data) => navigate(`/tasks/${data.task_id}`),
      onError: (err) => {
        if (
          err instanceof ApiError &&
          err.code === "invalid_input" &&
          isInvalidInputData(err.data)
        ) {
          // Fields without an input of their own (e.g. "body") render form-level.
          const known = new Set(["prompt", "task_type", "params", "lane"]);
          const key = known.has(err.data.field) ? err.data.field : "";
          setFieldErrors({ [key]: err.data.reason });
          if (key === "params" || key === "lane") setAdvancedOpen(true);
        } else if (err instanceof ApiError) {
          setFieldErrors({ "": err.message });
        }
      },
    });
  };

  const onSubmit = (e: FormEvent): void => {
    e.preventDefault();
    submit();
  };

  // Matches the detail-page composer: Ctrl/Cmd+Enter submits.
  const onPromptKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>): void => {
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey) && prompt.trim() !== "") {
      e.preventDefault();
      submit();
    }
  };

  const err = (field: string): string | undefined => fieldErrors[field];

  return (
    <section
      data-testid="task-create-page"
      className="flex h-full flex-col items-center justify-center gap-6"
    >
      <h1 className="text-3xl font-semibold text-foreground">What should we build?</h1>

      <form onSubmit={onSubmit} className="flex w-full max-w-2xl flex-col gap-3">
        {err("") ? (
          <p data-testid="form-error" className="text-sm text-destructive">
            {err("")}
          </p>
        ) : null}

        {/* Composer card */}
        <div className="flex flex-col gap-2 rounded-xl border border-border bg-card p-3 shadow-sm focus-within:border-ring">
          <Textarea
            data-testid="prompt-input"
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            onKeyDown={onPromptKeyDown}
            rows={4}
            placeholder="Describe the task…"
            aria-label="Prompt"
            className="resize-none border-0 bg-transparent p-1 shadow-none focus-visible:ring-0"
          />
          <div className="flex items-center gap-2">
            {/* Task-type chips */}
            <div
              className="flex flex-1 items-center gap-2"
              role="radiogroup"
              aria-label="Task type"
            >
              {TASK_TYPES.map((t) => (
                <button
                  key={t}
                  type="button"
                  role="radio"
                  aria-checked={taskType === t}
                  data-testid="task-type-chip"
                  data-task-type={t}
                  onClick={() => setTaskType(t)}
                  className={cn(
                    "rounded-full border px-3 py-1 text-xs transition-colors",
                    taskType === t
                      ? "border-primary bg-primary text-primary-foreground"
                      : "border-border text-muted-foreground hover:bg-accent hover:text-accent-foreground",
                  )}
                >
                  {t}
                </button>
              ))}
            </div>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="gap-1.5 text-xs text-muted-foreground"
              data-testid="advanced-toggle"
              aria-expanded={advancedOpen}
              onClick={() => setAdvancedOpen((v) => !v)}
            >
              <Settings2 className="size-3.5" aria-hidden />
              Advanced
            </Button>
            <Button
              type="submit"
              size="icon"
              className="size-9 rounded-full"
              disabled={mutation.isPending || prompt.trim() === ""}
              aria-label="Create task"
              data-testid="submit-button"
            >
              <ArrowUp className="size-4" aria-hidden />
            </Button>
          </div>
        </div>
        {err("prompt") ? <span className="text-xs text-destructive">{err("prompt")}</span> : null}
        {err("task_type") ? (
          <span className="text-xs text-destructive">{err("task_type")}</span>
        ) : null}

        {/* Advanced disclosure: params / lane */}
        {advancedOpen ? (
          <div
            data-testid="advanced-panel"
            className="flex flex-col gap-4 rounded-xl border border-border bg-card p-4"
          >
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="params">Params (JSON, optional)</Label>
              <Textarea
                id="params"
                data-testid="params-input"
                value={paramsText}
                onChange={(e) => setParamsText(e.target.value)}
                rows={3}
                className="font-mono"
              />
              {err("params") ? (
                <span className="text-xs text-destructive">{err("params")}</span>
              ) : null}
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="lane">Lane (optional)</Label>
              <Input
                id="lane"
                data-testid="lane-input"
                value={lane}
                onChange={(e) => setLane(e.target.value)}
              />
              {err("lane") ? <span className="text-xs text-destructive">{err("lane")}</span> : null}
            </div>
          </div>
        ) : null}
      </form>
    </section>
  );
}
