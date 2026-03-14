# main-agent Specification

## Purpose
TBD - created by archiving change deepagent-research-report. Update Purpose after archive.
## Requirements
### Requirement: Accept research topic and run pipeline
The main agent SHALL accept a user-supplied research topic string, create a `DockerSandbox` via `DockerSandboxProvider`, invoke the web-research subagent to gather information via streaming, pass the results and the sandbox to the html-report subagent, print the path of the generated HTML report to the caller after streaming completes, and stop the sandbox in a `finally` block.

#### Scenario: Successful end-to-end run
- **WHEN** the user calls `main()` (or the CLI entry point) with a non-empty topic string
- **THEN** a Docker sandbox is created, the pipeline runs, intermediate chunks are streamed to stdout, `report.html` is downloaded from the sandbox to the host, the local path to the downloaded file is printed, and the sandbox is stopped on completion

#### Scenario: Missing API key
- **WHEN** `DASHSCOPE_API_KEY` environment variable is not set
- **THEN** the agent raises a clear error before making any API call

#### Scenario: Pipeline error triggers sandbox cleanup
- **WHEN** the pipeline raises an exception after the sandbox is created
- **THEN** `sandbox.stop()` is still called and the container is removed

### Requirement: Use deepseek-v3.2 via ChatTongyi
All agents in the pipeline SHALL be configured with `ChatTongyi(model="deepseek-v3.2")` sourced from `langchain_community.chat_models`.

#### Scenario: Model configuration
- **WHEN** any agent node is initialized
- **THEN** the underlying LLM instance is `ChatTongyi` with `model="deepseek-v3.2"`

### Requirement: Delegate to subagents
The main agent SHALL delegate web research to the `web-research` subagent and report generation to the `html-report` subagent, passing the `DockerSandbox` instance both as `backend` to `create_deep_agent` and to `build_html_report_subagent()`.

#### Scenario: Delegation to web-research subagent
- **WHEN** the main agent receives a topic
- **THEN** it calls the web-research subagent with that topic and waits for structured results

#### Scenario: Delegation to html-report subagent with sandbox
- **WHEN** the main agent receives research results
- **THEN** it calls `build_html_report_subagent(sandbox)` with the active sandbox and passes the research results to that subagent

### Requirement: Use DockerSandbox as agent backend
The main agent SHALL pass the `DockerSandbox` as `backend` to `create_deep_agent` so all of the main agent's filesystem tools and shell commands are executed inside the Docker container.

#### Scenario: Main agent filesystem tools use sandbox
- **WHEN** `create_deep_agent` is called with `backend=sandbox`
- **THEN** all deepagents filesystem tools (`write_file`, `read_file`, `edit_file`, `glob`, `grep`) route through the sandbox container

### Requirement: Download report from sandbox after pipeline succeeds
After the pipeline completes successfully, `main()` SHALL download `/workspace/report.html` from the sandbox to the host filesystem using `sandbox.download_files()`, save it locally (e.g., `report.html` in the current working directory), and print the local file path.

#### Scenario: Successful download
- **WHEN** the pipeline completes without error and `/workspace/report.html` exists in the sandbox
- **THEN** `main()` calls `sandbox.download_files(["/workspace/report.html"])`, writes the returned bytes to `report.html` on the host, and prints the absolute local path

#### Scenario: Download error
- **WHEN** `sandbox.download_files()` returns a `FileDownloadResponse` with a non-None `error` field
- **THEN** `main()` prints a warning with the error message instead of crashing

