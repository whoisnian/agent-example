## Context

Currently `main()` calls `agent.ainvoke(...)`, which blocks until the entire pipeline finishes and returns only the final message. There is no visibility into intermediate steps—LLM replies, tool invocations, token counts, or which subagent is active—making debugging and monitoring difficult.

LangGraph's `astream` API with `stream_mode="messages"` and `subgraphs=True` surfaces every message chunk as it is generated, including subgraph namespace information that identifies the originating agent node.

## Goals / Non-Goals

**Goals:**
- Replace `ainvoke` with `astream(stream_mode="messages", subgraphs=True, version="v2")` in `main()`
- Print each streamed chunk with: agent/subgraph name, node type (llm or tool), text content, token usage (when present), and tool calls (when present)
- Keep output human-readable in the terminal

**Non-Goals:**
- Structured log output (JSON/NDJSON) or log file persistence
- Changes to subagents, model config, or any module other than `main.py`
- Suppressing the final result line already printed

## Decisions

### Use `astream` with `subgraphs=True` and `version="v2"`

With `version="v2"`, each yielded item is a dict `{'type', 'ns', 'data': (token, metadata)}`. Non-`messages` events (e.g. metadata events) are skipped; only `type == "messages"` events carry printable token and metadata.

`version="v2"` is the current recommended LangGraph streaming protocol.

**Alternative considered**: `stream_mode="updates"` — yields graph state updates instead of message chunks; harder to extract human-readable text incrementally.

### Determine node type from `metadata['langgraph_node']`

The event metadata dict carries `'langgraph_node'` with values like `'model'` or `'tools'`. Using this field is more reliable and readable than inspecting chunk class types, and exposes the exact graph node name for display.

**Alternative considered**: class-based dispatch on `AIMessageChunk` / `ToolMessage` — works but loses the node name and requires importing LangChain message types.

### Derive agent name from `metadata['lc_agent_name']`

`metadata['lc_agent_name']` is set by the DeepAgents runtime for each subgraph and directly contains the agent's declared name (e.g. `'web-research-agent'`). This is more reliable than parsing the `ns` tuple.

### Per-event timing header

Each event is prefixed with an incrementing index, wall-clock timestamp, and `+last/total` elapsed seconds. This makes it easy to identify slow nodes without external tooling.

### Token usage from `response_metadata['token_usage']`

The DashScope/ChatTongyi provider exposes token counts under `token.response_metadata['token_usage']` rather than the LangChain-standard `usage_metadata` field. The code checks for the provider-specific key.

## Risks / Trade-offs

- [Chunk volume] Streaming produces many per-event lines that may flood the terminal for long runs → Acceptable for a debug/development feature; no mitigation needed
- [Token usage availability] `response_metadata['token_usage']` is only present on final response tokens for the ChatTongyi/DashScope provider → Print token usage only when the key exists; silently skip otherwise
- [API stability] `version="v2"` is the current recommended protocol but may change in future LangGraph releases → Pinned by existing `pyproject.toml` dependency ranges
