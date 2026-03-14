## ADDED Requirements

### Requirement: Stream agent execution and print each chunk
The main execution loop SHALL use `agent.astream(stream_mode="messages", subgraphs=True, version="v2")` and print each yielded chunk in a readable format before printing the final result.

#### Scenario: LLM text chunk printed
- **WHEN** the agent streams an `AIMessageChunk` with non-empty text content
- **THEN** the output line SHALL include the agent/subgraph name, node type `llm`, and the text content

#### Scenario: Tool call chunk printed
- **WHEN** the agent streams an `AIMessageChunk` that contains one or more tool calls
- **THEN** the output line SHALL include the agent/subgraph name, node type `tool-call`, and the tool name(s) with arguments

#### Scenario: Tool result chunk printed
- **WHEN** the agent streams a `ToolMessage`
- **THEN** the output line SHALL include the agent/subgraph name, node type `tool-result`, and a truncated preview of the result content

#### Scenario: Token usage printed when available
- **WHEN** a streamed chunk contains non-None `usage_metadata`
- **THEN** the output SHALL include input and output token counts alongside the chunk line

#### Scenario: Agent name derived from namespace
- **WHEN** `subgraphs=True` yields a `(namespace, chunk)` pair with a non-empty namespace tuple
- **THEN** the printed agent name SHALL be the last segment of the namespace path

#### Scenario: Top-level messages labeled
- **WHEN** a chunk is yielded with an empty namespace (top-level graph)
- **THEN** the agent name SHALL be printed as `main`
