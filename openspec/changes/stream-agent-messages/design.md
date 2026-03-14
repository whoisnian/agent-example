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

With `subgraphs=True`, each yielded item is a `(namespace, chunk)` pair where `namespace` is a tuple of graph-node path strings identifying the active subgraph (e.g. `("main-agent:subagent:web-research",)`). This gives the agent/node name without parsing message metadata.

`version="v2"` uses the current LangGraph streaming protocol, which is stable and recommended for new code.

**Alternative considered**: `stream_mode="updates"` — yields graph state updates instead of message chunks; harder to extract human-readable text incrementally.

### Determine node type from chunk class

- `AIMessageChunk` with non-empty `tool_calls` → type `tool-call`
- `AIMessageChunk` with text content → type `llm`
- `ToolMessage` → type `tool-result`
- Other → type `event`

This avoids relying on fragile metadata fields and works with the LangGraph message type hierarchy.

### Derive agent name from namespace tail

Use `namespace[-1].split(":")[-1]` to extract the leaf node name from the full namespace path. Falls back to `"main"` when namespace is empty (top-level graph messages).

## Risks / Trade-offs

- [Chunk volume] Streaming produces many small chunks that may flood the terminal for long runs → Acceptable for a debug/development feature; no mitigation needed
- [Token usage availability] `usage_metadata` is only present on the final chunk of a response in most providers → Print token usage only when the field is non-None; silently skip otherwise
- [API stability] `version="v2"` is the current recommended protocol but may change in future LangGraph releases → Pinned by existing `pyproject.toml` dependency ranges
