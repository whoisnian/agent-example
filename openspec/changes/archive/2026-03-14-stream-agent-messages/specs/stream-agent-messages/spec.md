## ADDED Requirements

### Requirement: Stream agent execution and print structured event output
The main execution loop SHALL use `agent.astream(stream_mode="messages", subgraphs=True, version="v2")` and print each yielded `messages`-type event with a structured header and per-field output.

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

#### Scenario: Content printed truncated
- **WHEN** a messages event is printed
- **THEN** `token.content` SHALL be printed through `truncate_str()` which caps at 200 chars and appends the original length

#### Scenario: Token usage printed when available
- **WHEN** `token.response_metadata` contains a `'token_usage'` key
- **THEN** the full token usage dict SHALL be printed on its own line

#### Scenario: Tool calls printed
- **WHEN** `token.tool_calls` is non-empty
- **THEN** each tool call SHALL be printed; `write_todos` calls SHALL be formatted via `format_todos()`; all other calls SHALL print name and truncated args

### Requirement: Streaming utility helpers in utils.py
`utils.py` SHALL export `truncate_str(s, max_len=200)` and `format_todos(todos)` helpers used by the streaming output logic.

#### Scenario: truncate_str shortens long strings
- **WHEN** a string longer than `max_len` is passed to `truncate_str`
- **THEN** the returned string SHALL be capped at `max_len` chars with newlines replaced by spaces and the original length appended as `... [N truncated]`

#### Scenario: format_todos renders todo list
- **WHEN** a list of todo dicts with `status` and `content` keys is passed to `format_todos`
- **THEN** the returned string SHALL render each item as a markdown checkbox: `[x]` for completed, `[-]` for in_progress, `[ ]` otherwise
