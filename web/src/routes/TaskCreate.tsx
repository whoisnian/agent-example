import type { JSX } from "react";
import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
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

export function TaskCreate(): JSX.Element {
  const navigate = useNavigate();
  const mutation = useCreateTaskMutation();

  const [title, setTitle] = useState("");
  const [taskType, setTaskType] = useState<string>(TASK_TYPES[0]);
  const [prompt, setPrompt] = useState("");
  const [paramsText, setParamsText] = useState("");
  const [lane, setLane] = useState("");

  // field name -> message; key "" / "body" is a form-level error.
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const onSubmit = (e: FormEvent): void => {
    e.preventDefault();
    setFieldErrors({});

    let params: unknown;
    if (paramsText.trim() !== "") {
      try {
        params = JSON.parse(paramsText);
      } catch {
        setFieldErrors({ params: "params must be valid JSON" });
        return;
      }
    }

    const body: CreateTaskRequest = { title, task_type: taskType, prompt };
    if (params !== undefined) body.params = params;
    if (lane.trim() !== "") body.lane = lane.trim();

    mutation.mutate(body, {
      onSuccess: (data) => navigate(`/tasks/${data.task_id}`),
      onError: (err) => {
        if (err instanceof ApiError && err.code === "invalid_input" && isInvalidInputData(err.data)) {
          const key = err.data.field === "body" ? "" : err.data.field;
          setFieldErrors({ [key]: err.data.reason });
        } else if (err instanceof ApiError) {
          setFieldErrors({ "": err.message });
        }
      },
    });
  };

  const err = (field: string): string | undefined => fieldErrors[field];

  return (
    <section data-testid="task-create-page">
      <h1 className="mb-4 text-2xl font-semibold text-foreground">New task</h1>
      <form onSubmit={onSubmit} className="flex max-w-xl flex-col gap-4">
        {err("") ? (
          <p data-testid="form-error" className="text-sm text-destructive">
            {err("")}
          </p>
        ) : null}

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="title">Title</Label>
          <Input
            id="title"
            data-testid="title-input"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
          />
          {err("title") ? (
            <span className="text-xs text-destructive">{err("title")}</span>
          ) : null}
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="task-type">Task type</Label>
          <select
            id="task-type"
            data-testid="task-type-select"
            value={taskType}
            onChange={(e) => setTaskType(e.target.value)}
            className="h-10 rounded-md border border-input bg-background px-3 py-2 text-sm text-foreground"
          >
            {TASK_TYPES.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
          {err("task_type") ? (
            <span className="text-xs text-destructive">{err("task_type")}</span>
          ) : null}
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="prompt">Prompt</Label>
          <Textarea
            id="prompt"
            data-testid="prompt-input"
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            rows={4}
          />
          {err("prompt") ? (
            <span className="text-xs text-destructive">{err("prompt")}</span>
          ) : null}
        </div>

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
        </div>

        <div>
          <Button type="submit" disabled={mutation.isPending} data-testid="submit-button">
            {mutation.isPending ? "Creating…" : "Create task"}
          </Button>
        </div>
      </form>
    </section>
  );
}
