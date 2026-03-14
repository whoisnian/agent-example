## 1. Replace `ainvoke` with `astream` in `main()`

- [x] 1.1 In `main.py`, replace `result = await agent.ainvoke(...)` with `async for event in agent.astream(..., stream_mode="messages", subgraphs=True, version="v2"):` where each event is a dict with `type`, `ns`, and `data` keys
- [x] 1.2 Add `time` module import and track per-event and total elapsed seconds using `time.time()`

## 2. Implement per-event printing logic

- [x] 2.1 Extract agent name from `metadata['lc_agent_name']` and node name from `metadata['langgraph_node']`
- [x] 2.2 Print a header line per event: `{event_type}.{idx} ---- {timestamp} ---- (+{last}s/{total}s)`
- [x] 2.3 Print agent name, node name, and for `model` nodes print `metadata['ls_model_name']`; for `tools` nodes print `token.name`
- [x] 2.4 Print content via `truncate_str(token.content)` (add `truncate_str` to `utils.py`)
- [x] 2.5 When `token.response_metadata` contains `'token_usage'`, print the token usage dict
- [x] 2.6 When `token.tool_calls` is non-empty, print each tool call; use `format_todos()` for `write_todos` calls, else print name and truncated args (add `format_todos` to `utils.py`)

## 3. Verify end-to-end behaviour

- [x] 3.1 Run the agent with a sample topic and confirm per-event headers and structured fields print during execution
- [x] 3.2 Confirm agent name, node, content, token usage, and tool calls all appear correctly in the output
