## Why

Debugging the deep agent pipeline is difficult because `ainvoke` swallows all intermediate LLM messages, tool calls, and token usage — leaving only the final output visible. Switching to streaming mode gives real-time visibility into what each node is doing, making the system significantly easier to develop and troubleshoot.

## What Changes

- Replace `agent.ainvoke(...)` in `main()` with `agent.astream(stream_mode="messages", subgraphs=True, version="v2")`
- Add a streaming output loop that processes and prints each streamed chunk in a readable format
- Print per-chunk metadata: agent/node name, node type (LLM vs tool), message content, token usage, and any tool calls

## Capabilities

### New Capabilities
- `stream-agent-messages`: Streams agent execution via `astream` and pretty-prints each LLM message and tool call chunk with agent name, node type, content, token usage, and tool call details

### Modified Capabilities
- `main-agent`: Execution model changes from `ainvoke` (blocking, single result) to `astream` (streaming, incremental chunks)

## Impact

- `main.py`: `main()` function rewritten to use `astream` loop instead of `ainvoke`
- No changes to subagents, model config, or the `utils.py` / `agents/` modules
- No new dependencies; `astream` is part of the existing `deepagents` / LangGraph interface
