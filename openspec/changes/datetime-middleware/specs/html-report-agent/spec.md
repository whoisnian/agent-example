## MODIFIED Requirements

### Requirement: Generate an HTML report from research results
The html-report subagent SHALL accept structured research results and a `DockerSandbox` instance, produce a self-contained HTML file (`report.html`), write it to `/workspace` inside the sandbox using deepagents's built-in `write_file` tool, and include the task start datetime — injected into its system prompt by `DatetimeMiddleware` — as a visible timestamp in the report's footer.

#### Scenario: Successful report generation
- **WHEN** the subagent is invoked with non-empty research results and uses a `DockerSandbox` as its backend
- **THEN** it calls the deepagents `write_file` tool with path `/workspace/report.html` and the generated HTML content, writing the file into the sandbox container

#### Scenario: Report content completeness
- **WHEN** the report is generated
- **THEN** it includes a title derived from the research topic, the research summary, and a "Generated on \<timestamp>" footer where `<timestamp>` is the `Task started at:` value injected by `DatetimeMiddleware`

#### Scenario: Overwrite on re-run
- **WHEN** `/workspace/report.html` already exists in the sandbox
- **THEN** the subagent replaces it with the newly generated report

## ADDED Requirements

### Requirement: Apply DatetimeMiddleware to the html-report agent
`build_html_report_subagent(sandbox)` SHALL instantiate `DatetimeMiddleware` and pass it via the `middleware` parameter of `create_agent()` so the agent's system prompt receives the injected task start datetime before each model call.

#### Scenario: DatetimeMiddleware in middleware list
- **WHEN** `build_html_report_subagent(sandbox)` constructs the agent
- **THEN** `create_agent()` is called with `middleware=[DatetimeMiddleware()]`

#### Scenario: Timestamp present in system prompt at model call time
- **WHEN** `DatetimeMiddleware` runs `wrap_model_call` for the html-report agent
- **THEN** the system prompt contains the `"Task started at: ..."` line before the model is called
