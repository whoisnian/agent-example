## 1. Dependency

- [ ] 1.1 Add `langgraph-checkpoint-sqlite` to `pyproject.toml` dependencies and run `uv lock`

## 2. Context Schema

- [ ] 2.1 Add `thread_id: str` field to `CustomContext` dataclass in `context.py`

## 3. CLI and Checkpointer in main.py

- [ ] 3.1 Parse `--thread-id` optional flag from `sys.argv` using `argparse`; remaining args become the topic string
- [ ] 3.2 Generate a UUID `thread_id` when `--thread-id` is not supplied
- [ ] 3.3 Print the active `thread_id` to stdout before streaming begins
- [ ] 3.4 Open `SqliteSaver` from `checkpoints.db` as a context manager wrapping the agent lifecycle
- [ ] 3.5 Pass `checkpointer=saver` to `create_deep_agent()`
- [ ] 3.6 Pass `config={"configurable": {"thread_id": thread_id}}` to `agent.astream()`
- [ ] 3.7 Pass `context=CustomContext(start_time=start_time, thread_id=thread_id)` to `agent.astream()`
- [ ] 3.8 When resuming (thread_id supplied), pass `{"messages": [{"role": "user", "content": topic}]}` as the input to `agent.astream()` to append the new user message to prior history
