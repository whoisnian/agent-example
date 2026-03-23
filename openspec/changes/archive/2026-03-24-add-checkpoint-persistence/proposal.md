## Why

Each run of `main.py` starts a fresh agent graph with no memory of prior interactions, making it impossible to resume a conversation or continue a long-running task. Adding LangGraph's SQLite checkpointer enables persistent, resumable agent threads identified by a `thread_id`.

## What Changes

- Add `langgraph-checkpoint-sqlite` as a project dependency.
- Extend `CustomContext` with a `thread_id: str` field.
- Update `main()` to accept an optional positional `thread_id` CLI argument.
  - If omitted, generate a new UUID and start a fresh stream.
  - If provided, load the existing checkpoint and continue the stream with the new user input appended to the prior message history.
- Wire the `SqliteSaver` checkpointer into `create_deep_agent()` via the `checkpointer` parameter.
- Pass `{"configurable": {"thread_id": thread_id}}` as the `config` argument to `agent.astream()`.
- Print the active `thread_id` at the start of every run so the user can reference it to resume later.

## Capabilities

### New Capabilities
- `checkpoint-persistence`: SQLite-backed LangGraph checkpointer that persists graph state keyed by `thread_id`, enabling session resume across `main.py` invocations.

### Modified Capabilities
- `main-agent`: Accepts an optional `thread_id` CLI argument; generates one if absent; wires the checkpointer; passes `thread_id` in `config` and `context`.
- `datetime-middleware`: `CustomContext` dataclass gains a `thread_id: str` field.

## Impact

- **Dependencies**: `langgraph-checkpoint-sqlite` added to `pyproject.toml`.
- **`context.py`**: `CustomContext` gains `thread_id: str`.
- **`main.py`**: CLI argument parsing, checkpointer setup, `config` passed to `astream()`.
- **`main-agent` spec**: Updated to reflect thread_id CLI argument and checkpointer wiring.
- **`datetime-middleware` spec**: Updated to reflect new `thread_id` field on `CustomContext`.
- No changes to subagents, sandbox, or skills.
