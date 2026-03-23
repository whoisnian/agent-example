## Context

`main.py` currently creates a stateless agent graph on every invocation. All conversation history is lost when the process exits, making it impossible to resume work across runs. LangGraph provides a `SqliteSaver` checkpointer in the `langgraph-checkpoint-sqlite` package that persists full graph state (messages, node outputs, intermediate values) to a local SQLite database keyed by `thread_id` and `checkpoint_id`.

The existing `CustomContext` dataclass lives in `context.py` and carries `start_time: datetime`. It is passed to `agent.astream()` and read by `DatetimeMiddleware`. Adding `thread_id` here makes it available to any future middleware without additional plumbing.

## Goals / Non-Goals

**Goals:**
- Persist agent graph state between `main.py` invocations using SQLite.
- Allow the user to resume a prior conversation by passing its `thread_id` on the CLI.
- Auto-generate a UUID `thread_id` when none is supplied, and print it so the user can reuse it.
- Append new user input to the existing message history when resuming a thread.
- Add `thread_id: str` to `CustomContext`.

**Non-Goals:**
- Multi-user or server-side session management.
- Distributed or cloud checkpointing.
- Changing subagent behaviour, sandbox, or skills.
- Deleting or listing existing checkpoints.

## Decisions

### Decision: Use `langgraph-checkpoint-sqlite` with `SqliteSaver`
LangGraph's first-party SQLite saver integrates directly with `create_deep_agent()` via the `checkpointer` parameter — the same interface used for in-memory savers. No LangGraph internals need to be touched.

**Alternatives considered:**
- `MemorySaver`: in-process only; lost on exit.
- Custom pickle/JSON file: duplicates what the checkpointer already does; fragile.
- PostgreSQL saver: production-grade but requires a running server — overkill for a local CLI tool.

### Decision: CLI argument is positional, not a flag
`main.py` today reads `sys.argv[1:]` as the topic. The new signature treats the first argument as `thread_id` only when it looks like a UUID (or we adopt a two-argument form). The cleaner approach: make `thread_id` an explicit first argument and the topic everything after it, OR keep the topic as the only positional arg and add `--thread-id` as an option.

**Chosen**: `--thread-id` optional flag + topic as positional remainder. This is backward-compatible and unambiguous.

**Alternatives considered:**
- Positional first arg as thread_id: breaks existing one-argument usage ("What's LangChain?").
- Environment variable: less discoverable for interactive use.

### Decision: Checkpoint database path is `checkpoints.db` in the working directory
A fixed local path is simple and predictable. Users who need a different location can move the file or set a future env var.

### Decision: Resume by appending a new `HumanMessage` to the checkpoint state
When `thread_id` is provided, the graph is already checkpointed with prior messages. Passing `{"messages": [{"role": "user", "content": new_input}]}` to `astream()` appends to the existing message list rather than replacing it — this is the standard LangGraph resume pattern.

## Risks / Trade-offs

- [Schema drift] If the graph state schema changes (node names, message types), old checkpoints may be incompatible. → Mitigation: document that checkpoints are tied to a specific code version; old `.db` files can be deleted safely.
- [SQLite concurrency] Running two `main.py` processes with the same `thread_id` simultaneously can corrupt the checkpoint. → Mitigation: SQLite's WAL mode handles single-writer contention; the CLI is inherently single-user.
- [Disk growth] Long-running threads accumulate checkpoint rows. → Mitigation: acceptable for a local dev tool; users can delete `checkpoints.db` to reset.
