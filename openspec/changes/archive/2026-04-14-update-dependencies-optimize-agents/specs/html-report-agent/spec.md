## MODIFIED Requirements

### Requirement: Expose only write_file tool to the html-report agent
`build_html_report_subagent(sandbox)` SHALL instantiate `FilesystemMiddleware(backend=sandbox)`, extract the `write_file` tool from its `tools` list by name, and pass it to `create_agent()` via the `tools` parameter — without passing the middleware itself. The agent's tool list SHALL contain only `write_file`. All imports and APIs SHALL be compatible with `deepagents>=0.5.2`.

#### Scenario: Agent tool list contains only write_file
- **WHEN** `build_html_report_subagent(sandbox)` constructs the agent
- **THEN** the agent is created with `tools=[write_file_tool]` and no `middleware` containing filesystem tools, so only `write_file` is available

#### Scenario: Filesystem exploration tools are unavailable
- **WHEN** the html-report agent is running
- **THEN** it cannot call `ls`, `read_file`, `glob`, `grep`, or `execute` because those tools are not in its tool list

#### Scenario: build_html_report_subagent requires sandbox parameter
- **WHEN** `build_html_report_subagent(sandbox)` is called
- **THEN** the returned subagent's `write_file` tool is bound to that specific `sandbox` instance via `FilesystemMiddleware`

### Requirement: Apply DatetimeMiddleware to the html-report agent
`build_html_report_subagent(sandbox)` SHALL instantiate `DatetimeMiddleware` and pass it via the `middleware` parameter of `create_agent()` so the agent's system prompt receives the injected task start datetime before each model call. The `DatetimeMiddleware` API SHALL be compatible with `deepagents>=0.5.2` `AgentMiddleware`.

#### Scenario: DatetimeMiddleware in middleware list
- **WHEN** `build_html_report_subagent(sandbox)` constructs the agent
- **THEN** `create_agent()` is called with `middleware=[DatetimeMiddleware()]`

#### Scenario: Timestamp present in system prompt at model call time
- **WHEN** `DatetimeMiddleware` runs `wrap_model_call` for the html-report agent
- **THEN** the system prompt contains the `"Task started at: ..."` line before the model is called

### Requirement: Use deepseek-v3.2 via ChatTongyi
The html-report subagent SHALL be configured with `ChatTongyi(model="deepseek-v3.2")` from `langchain_community.chat_models`. The import path SHALL be valid with the latest `langchain-community` version.

#### Scenario: Model configuration
- **WHEN** the html-report subagent node is initialized
- **THEN** the underlying LLM is `ChatTongyi` with `model="deepseek-v3.2"`
