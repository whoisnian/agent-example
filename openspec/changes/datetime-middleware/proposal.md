## Why

Agents currently have no awareness of when a task started, making it impossible to include accurate timestamps in generated reports or logs. Adding a datetime middleware provides a clean, reusable way to inject start-time context into agent system prompts without scattering datetime logic across agent files.

## What Changes

- Introduce a new `DatetimeMiddleware` that reads a `start_time` value from agent context, formats it as a human-readable string, and appends it to the agent's system prompt.
- Extend the main agent pipeline to define a `context_schema` with a `start_time` field and pass `start_time=datetime.now()` when calling `agent.astream()`.
- Update the html-report agent's system prompt to instruct it to include the task start datetime (provided via middleware) as a visible timestamp in the generated HTML report.

## Capabilities

### New Capabilities
- `datetime-middleware`: A middleware that reads a `start_time` datetime value from agent context, formats it into a readable string, and appends it to the system prompt so agents can reference the task start time.

### Modified Capabilities
- `main-agent`: Must declare a `context_schema` with a `start_time: datetime` field and supply `start_time=datetime.now()` when invoking `agent.astream()`.
- `html-report-agent`: Must use the start datetime injected into its system prompt by `DatetimeMiddleware` to include a human-readable timestamp in the generated HTML report.

## Impact

- `agents/` — new `datetime_middleware.py` module; `html_report.py` updated to use `DatetimeMiddleware`.
- `main.py` — `context_schema` added, `start_time` passed to `astream()`.
- No external dependencies added; uses Python's standard `datetime` module.
- No breaking changes to the public API or CLI interface.
