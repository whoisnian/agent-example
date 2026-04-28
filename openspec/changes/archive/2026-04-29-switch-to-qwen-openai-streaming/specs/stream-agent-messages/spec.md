## MODIFIED Requirements

### Requirement: Stream agent execution and print structured event output
The main execution loop SHALL use `agent.astream(stream_mode="messages", subgraphs=True, version="v2")` and print each yielded `messages`-type event with a structured header and per-field output. Additionally, when a model node yields a message chunk with non-empty `content`, the content SHALL be printed immediately with `flush=True` to achieve real-time incremental token streaming to the console.

#### Scenario: Event header printed
- **WHEN** any event is yielded from the stream
- **THEN** the output SHALL include an incrementing index, event type, wall-clock timestamp, and elapsed time since the previous event and since stream start (format: `+Xs/Ys`)

#### Scenario: Agent and node fields printed for messages events
- **WHEN** an event has `type == "messages"`
- **THEN** the output SHALL include `agent` (from `metadata['lc_agent_name']`) and `node` (from `metadata['langgraph_node']`)

#### Scenario: Model name printed for model nodes
- **WHEN** `metadata['langgraph_node'] == 'model'`
- **THEN** the output SHALL include the model name from `metadata['ls_model_name']`

#### Scenario: Tool name printed for tool nodes
- **WHEN** `metadata['langgraph_node'] == 'tools'`
- **THEN** the output SHALL include the tool name from `token.name`

#### Scenario: Incremental token content streamed to console
- **WHEN** a model node yields a message chunk with non-empty `token.content`
- **THEN** the content SHALL be printed immediately using `print(token.content, end="", flush=True)` so the user sees output character-by-character as it arrives

#### Scenario: Content printed truncated
- **WHEN** a messages event is printed
- **THEN** `token.content` SHALL be printed through `truncate_str()` which caps at 200 chars and appends the original length

#### Scenario: Token usage printed when available
- **WHEN** `token.response_metadata` contains a `'token_usage'` key
- **THEN** the full token usage dict SHALL be printed on its own line

#### Scenario: Tool calls printed
- **WHEN** `token.tool_calls` is non-empty
- **THEN** each tool call SHALL be printed; `write_todos` calls SHALL be formatted via `format_todos()`; all other calls SHALL print name and truncated args
