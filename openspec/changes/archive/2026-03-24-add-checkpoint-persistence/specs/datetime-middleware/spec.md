## MODIFIED Requirements

### Requirement: Provide CustomContext dataclass
`context.py` SHALL export a `CustomContext` dataclass with a `start_time: datetime` field and a `thread_id: str` field so callers can construct a typed context value and supply it as `context_schema` / `context` to `create_deep_agent()` or `create_agent()`.

#### Scenario: CustomContext instantiation with thread_id
- **WHEN** `CustomContext(start_time=datetime.now(), thread_id="some-uuid")` is constructed
- **THEN** the instance holds both the supplied `start_time` and `thread_id` values and is accepted by `create_deep_agent(context_schema=CustomContext)`

#### Scenario: thread_id field accessible on context
- **WHEN** middleware or agent code accesses `request.runtime.context.thread_id`
- **THEN** it receives the `thread_id` string passed to `agent.astream()`
