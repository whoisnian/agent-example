# datetime-middleware Specification

## Purpose
Provides `DatetimeMiddleware` and `CustomContext` for injecting the task start time into agent system prompts at model-call time.

## Requirements
### Requirement: Inject task start datetime into the agent system prompt
`DatetimeMiddleware` SHALL read a `start_time: datetime` value from `request.runtime.context`, format it as an RFC3339 string (e.g., `2026-03-15T10:30:00+08:00`) by calling `start_time.astimezone().isoformat(timespec="seconds")`, and append the formatted timestamp to the agent's system prompt before every model call, so the agent can reference the task start time in its output.

#### Scenario: Context carries start_time
- **WHEN** the agent's `runtime.context` is a `CustomContext` instance with a non-None `start_time`
- **THEN** the string `"Task started at: <RFC3339>"` is appended to the system prompt and the original system prompt content is preserved

#### Scenario: Context is None or missing start_time
- **WHEN** `runtime.context` is `None` or does not have a `start_time` attribute
- **THEN** the system prompt is not modified and the model call proceeds unchanged

### Requirement: Provide CustomContext dataclass
`context.py` SHALL export a `CustomContext` dataclass with a `start_time: datetime` field and a `thread_id: str` field so callers can construct a typed context value and supply it as `context_schema` / `context` to `create_deep_agent()` or `create_agent()`.

#### Scenario: CustomContext instantiation with thread_id
- **WHEN** `CustomContext(start_time=datetime.now(), thread_id="some-uuid")` is constructed
- **THEN** the instance holds both the supplied `start_time` and `thread_id` values and is accepted by `create_deep_agent(context_schema=CustomContext)`

#### Scenario: thread_id field accessible on context
- **WHEN** middleware or agent code accesses `request.runtime.context.thread_id`
- **THEN** it receives the `thread_id` string passed to `agent.astream()`

### Requirement: Support both sync and async model call wrapping
`DatetimeMiddleware` SHALL implement both `wrap_model_call` and `awrap_model_call` so it can be used with synchronous and asynchronous agent graphs without raising an error.

#### Scenario: Async wrapping
- **WHEN** `awrap_model_call` is called with a `ModelRequest` and an async handler
- **THEN** the middleware injects the datetime into the system prompt and awaits the handler with the modified request
