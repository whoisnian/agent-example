## 1. Replace `ainvoke` with `astream` in `main()`

- [ ] 1.1 In `main.py`, replace `result = await agent.ainvoke(...)` with an `async for namespace, chunk in agent.astream(..., stream_mode="messages", subgraphs=True, version="v2"):` loop
- [ ] 1.2 Track the last `AIMessage` (non-chunk) across the stream to retain the final result for printing after the loop

## 2. Implement per-chunk printing logic

- [ ] 2.1 Extract the agent name from the `namespace` tuple: use `namespace[-1].split(":")[-1]` when non-empty, fall back to `"main"`
- [ ] 2.2 Detect node type from chunk class: `AIMessageChunk` with `tool_calls` → `tool-call`; `AIMessageChunk` with text → `llm`; `ToolMessage` → `tool-result`; else `event`
- [ ] 2.3 Print agent name, node type, and message content for each chunk in a readable one-line format
- [ ] 2.4 When `chunk.usage_metadata` is not `None`, append input/output token counts to the printed line
- [ ] 2.5 When node type is `tool-call`, print tool name(s) and arguments instead of (or alongside) raw content
- [ ] 2.6 When node type is `tool-result`, truncate `ToolMessage.content` to a short preview (e.g. 120 chars)

## 3. Verify end-to-end behaviour

- [ ] 3.1 Run the agent with a sample topic and confirm intermediate chunks are printed during execution
- [ ] 3.2 Confirm the final `report.html` path is still printed after the stream completes
