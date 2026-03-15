## ADDED Requirements

### Requirement: Inject task start datetime into the agent system prompt
`DatetimeMiddleware` SHALL read a `start_time: datetime` value from `request.runtime.context`, format it as an RFC3339 string (e.g., `2026-03-15T10:30:00+08:00`) by calling `start_time.astimezone().isoformat(timespec="seconds")`, and append the formatted timestamp to the agent's system prompt before every model call, so the agent can reference the task start time in its output.

#### Scenario: Context carries start_time
- **WHEN** the agent's `runtime.context` is a `DatetimeContext` instance with a non-None `start_time`
- **THEN** the string `"Task started at: <formatted_datetime>"` is appended to the system prompt and the original system prompt content is preserved

#### Scenario: Context is None or missing start_time
- **WHEN** `runtime.context` is `None` or does not have a `start_time` attribute
- **THEN** the system prompt is not modified and the model call proceeds unchanged

### Requirement: Provide DatetimeContext dataclass
The module SHALL export a `DatetimeContext` dataclass with a single field `start_time: datetime` so callers can construct a typed context value and supply it as `context_schema` / `context` to `create_deep_agent()` or `create_agent()`.

#### Scenario: DatetimeContext instantiation
- **WHEN** `DatetimeContext(start_time=datetime.now())` is constructed
- **THEN** the instance holds the supplied `start_time` value and is accepted by `create_deep_agent(context_schema=DatetimeContext)`

### Requirement: Support both sync and async model call wrapping
`DatetimeMiddleware` SHALL implement both `wrap_model_call` and `awrap_model_call` so it can be used with synchronous and asynchronous agent graphs without raising an error.

#### Scenario: Async wrapping
- **WHEN** `awrap_model_call` is called with a `ModelRequest` and an async handler
- **THEN** the middleware injects the datetime into the system prompt and awaits the handler with the modified request
