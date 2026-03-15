# html-report-agent Specification

## Purpose
Accepts structured research results and a `DockerSandbox` instance, generates a self-contained HTML report, and writes it to `/workspace/report.html` inside the sandbox using deepagents's built-in `write_file` tool.
## Requirements
### Requirement: Generate an HTML report from research results
The html-report subagent SHALL accept structured research results and a `DockerSandbox` instance, produce a self-contained HTML file (`report.html`), and write it to `/workspace` inside the sandbox using deepagents's built-in `write_file` tool (provided by `FilesystemMiddleware` backed by the sandbox).

#### Scenario: Successful report generation
- **WHEN** the subagent is invoked with non-empty research results and uses a `DockerSandbox` as its backend
- **THEN** it calls the deepagents `write_file` tool with path `/workspace/report.html` and the generated HTML content, writing the file into the sandbox container

#### Scenario: Report content completeness
- **WHEN** the report is generated
- **THEN** it includes a title derived from the research topic, the research summary, and a timestamp

### Requirement: Expose only write_file tool to the html-report agent
`build_html_report_subagent(sandbox)` SHALL instantiate `FilesystemMiddleware(backend=sandbox)`, extract the `write_file` tool from its `tools` list by name, and pass it to `create_agent()` via the `tools` parameter — without passing the middleware itself. The agent's tool list SHALL contain only `write_file`.

#### Scenario: Agent tool list contains only write_file
- **WHEN** `build_html_report_subagent(sandbox)` constructs the agent
- **THEN** the agent is created with `tools=[write_file_tool]` and no `middleware` containing filesystem tools, so only `write_file` is available

#### Scenario: Filesystem exploration tools are unavailable
- **WHEN** the html-report agent is running
- **THEN** it cannot call `ls`, `read_file`, `glob`, `grep`, or `execute` because those tools are not in its tool list

#### Scenario: build_html_report_subagent requires sandbox parameter
- **WHEN** `build_html_report_subagent(sandbox)` is called
- **THEN** the returned subagent's `write_file` tool is bound to that specific `sandbox` instance via `FilesystemMiddleware`

### Requirement: Report is self-contained HTML
The generated report SHALL be a single standalone HTML file with all styling inlined (no external CSS or JS dependencies).

#### Scenario: Self-contained file
- **WHEN** `report.html` is opened in a browser without internet access
- **THEN** it renders correctly without any missing resources

### Requirement: Overwrite existing report
The html-report subagent SHALL overwrite `report.html` if it already exists in the sandbox workspace, without prompting.

#### Scenario: Overwrite on re-run
- **WHEN** `/workspace/report.html` already exists in the sandbox
- **THEN** the subagent replaces it with the newly generated report

### Requirement: Use deepseek-v3.2 via ChatTongyi
The html-report subagent SHALL be configured with `ChatTongyi(model="deepseek-v3.2")`.

#### Scenario: Model configuration
- **WHEN** the html-report subagent node is initialized
- **THEN** the underlying LLM is `ChatTongyi` with `model="deepseek-v3.2"`

