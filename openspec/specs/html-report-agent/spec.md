# html-report-agent Specification

## Purpose
TBD - created by archiving change deepagent-research-report. Update Purpose after archive.
## Requirements
### Requirement: Generate an HTML report from research results
The html-report subagent SHALL accept structured research results and a `DockerSandbox` instance, produce a self-contained HTML file (`report.html`), and write it to `/workspace` inside the sandbox using deepagents's built-in `write_file` tool (provided by `FilesystemMiddleware` backed by the sandbox).

#### Scenario: Successful report generation
- **WHEN** the subagent is invoked with non-empty research results and uses a `DockerSandbox` as its backend
- **THEN** it calls the deepagents `write_file` tool with path `/workspace/report.html` and the generated HTML content, writing the file into the sandbox container

#### Scenario: Report content completeness
- **WHEN** the report is generated
- **THEN** it includes a title derived from the research topic, the research summary, and a timestamp

### Requirement: Use FilesystemMiddleware with sandbox backend
`build_html_report_subagent(sandbox)` SHALL wire `FilesystemMiddleware(backend=sandbox)` into the html-report agent, replacing the custom `write_report_html` tool with deepagents's built-in filesystem tools routed through the sandbox.

#### Scenario: write_file routes through sandbox
- **WHEN** the subagent calls the deepagents `write_file` tool with `path="/workspace/report.html"`
- **THEN** the file is written inside the Docker container at `/workspace/report.html` via `sandbox.execute()`

#### Scenario: build_html_report_subagent requires sandbox parameter
- **WHEN** `build_html_report_subagent(sandbox)` is called
- **THEN** the returned subagent's filesystem middleware is bound to that specific `sandbox` instance

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

