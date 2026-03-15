## ADDED Requirements

### Requirement: Expose only write_file tool to the html-report agent
`build_html_report_subagent(sandbox)` SHALL instantiate `FilesystemMiddleware(backend=sandbox)`, extract the `write_file` tool from its `tools` list by name, and pass it to `create_agent()` via the `tools` parameter — without passing the middleware itself. The agent's tool list SHALL contain only `write_file`.

#### Scenario: Agent tool list contains only write_file
- **WHEN** `build_html_report_subagent(sandbox)` constructs the agent
- **THEN** the agent is created with `tools=[write_file_tool]` and no `middleware` containing filesystem tools, so only `write_file` is available

#### Scenario: Filesystem exploration tools are unavailable
- **WHEN** the html-report agent is running
- **THEN** it cannot call `ls`, `read_file`, `glob`, `grep`, or `execute` because those tools are not in its tool list

#### Scenario: Report content derived from input only
- **WHEN** the report is generated
- **THEN** all content (title, summary, facts) is derived exclusively from the research results passed in as input, not from any file read from the sandbox or workspace

#### Scenario: write_file is the sole tool call
- **WHEN** the subagent completes report generation
- **THEN** it has made exactly one tool call: `write_file` with path `/workspace/report.html` and the complete HTML content
