## 1. New Module: DatetimeMiddleware

- [ ] 1.1 Create `agents/datetime_middleware.py` with `DatetimeContext` dataclass (`start_time: datetime` field)
- [ ] 1.2 Implement `DatetimeMiddleware(AgentMiddleware)` with `_inject` helper that reads `request.runtime.context.start_time`, formats it as RFC3339 via `start_time.astimezone().isoformat(timespec="seconds")`, and appends `"Task started at: <ts>"` to the system prompt via `append_to_system_message`
- [ ] 1.3 Implement `wrap_model_call` (sync) calling `_inject` then `handler`
- [ ] 1.4 Implement `awrap_model_call` (async) calling `_inject` then `await handler`
- [ ] 1.5 Guard both methods: skip injection when `context` is `None` or has no `start_time`

## 2. Update html-report Agent

- [ ] 2.1 Import `DatetimeMiddleware` from `agents.datetime_middleware` in `agents/html_report.py`
- [ ] 2.2 Pass `middleware=[DatetimeMiddleware()]` to `create_agent()` in `build_html_report_subagent`
- [ ] 2.3 Update `_SYSTEM_PROMPT` to explicitly instruct the agent to use the `"Task started at:"` value from the system prompt as the timestamp in the report footer

## 3. Update Main Agent Pipeline

- [ ] 3.1 Import `datetime` from the standard library and `DatetimeContext` from `agents.datetime_middleware` in `main.py`
- [ ] 3.2 Capture `start_time = datetime.now()` at the start of the pipeline (before sandbox creation)
- [ ] 3.3 Pass `context_schema=DatetimeContext` to `create_deep_agent()`
- [ ] 3.4 Pass `context=DatetimeContext(start_time=start_time)` as a keyword argument to `agent.astream()`
