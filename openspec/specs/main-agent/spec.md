# main-agent Specification

## Purpose
Orchestrates the research pipeline: accepts a user-supplied topic, uploads project skills to the sandbox, delegates to the web-research and html-report subagents, shares the generated report via the share-html skill, downloads the report to the host, and prints the local path and shareable URL.
## Requirements
### Requirement: Accept research topic and run pipeline
The main agent SHALL accept a user-supplied research topic string, create a `DockerSandbox` via `DockerSandboxProvider`, upload the share-html `SKILL.md` to the sandbox before creating the agent, invoke the web-research subagent to gather information via streaming, pass the results and the sandbox to the html-report subagent, use the share-html skill to upload and share the report, print the path of the generated HTML report and the shareable URL to the caller after streaming completes, and stop the sandbox in a `finally` block.

#### Scenario: Successful end-to-end run
- **WHEN** the user calls `main()` (or the CLI entry point) with a non-empty topic string
- **THEN** a Docker sandbox is created, the share-html `SKILL.md` is uploaded to the sandbox, the pipeline runs (web-research → html-report → share-html), intermediate chunks are streamed to stdout, `report.html` is downloaded from the sandbox to the host, the local path to the downloaded file is printed, the shareable URL is printed, and the sandbox is stopped on completion

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

### Requirement: Upload share-html skill to sandbox before agent creation
Before creating the main agent, `main()` SHALL upload the `skills/share-html/SKILL.md` file to the sandbox at `/workspace/skills/share-html/SKILL.md` using `sandbox.upload_files()`.

#### Scenario: SKILL.md uploaded successfully
- **WHEN** `main()` creates the Docker sandbox
- **THEN** it reads `skills/share-html/SKILL.md` from the host and calls `sandbox.upload_files([("skills/share-html/SKILL.md", content)])` before calling `create_deep_agent()`

### Requirement: Wire share-html skill into main agent via skills parameter
The main agent SHALL be created with `skills=["/workspace/skills/"]` so `SkillsMiddleware` loads the share-html skill from the sandbox at runtime.

#### Scenario: SkillsMiddleware reads share-html skill
- **WHEN** `create_deep_agent()` is called with `skills=["/workspace/skills/"]`
- **THEN** the agent's context includes the share-html skill metadata and instructions read from `/workspace/skills/share-html/SKILL.md` in the sandbox

### Requirement: Use write_todos to plan pipeline before execution
The main agent's system prompt SHALL instruct it to call the `write_todos` tool as a preliminary planning step — outside of and before the numbered pipeline steps — listing all planned actions (web research, html report generation, share html, download report) before invoking any subagent or skill.

#### Scenario: Planning step precedes pipeline execution
- **WHEN** the main agent receives a research topic
- **THEN** its first action is a `write_todos` call enumerating the pipeline steps, and only afterward does it delegate to the web-research subagent

#### Scenario: Execution follows the planned sequence
- **WHEN** the main agent has called `write_todos`
- **THEN** it executes steps in the declared order: web-research subagent → html-report subagent → share-html skill → report path returned to user, without reordering or skipping steps

### Requirement: System prompt instructs agent to share the report
The main agent's system prompt SHALL instruct the agent to first call `write_todos` to plan all pipeline steps (as a preliminary action, not numbered as step 1), then execute the following numbered pipeline steps in order:
1. Use the web-research subagent to gather information about the topic.
2. Pass the full research results to the html-report subagent to generate an HTML report.
3. Use the `share-html` skill to upload and share the report, then report the shareable URL back to the user.
4. Report the path of the generated HTML file back to the user.

The system prompt SHALL NOT include the implementation details of the share-html skill (e.g., the curl command).

#### Scenario: System prompt references share-html skill by name
- **WHEN** the main agent is initialized
- **THEN** its system prompt includes a numbered step directing it to use the share-html skill, without embedding the curl command or upload URL

#### Scenario: Share step runs after report generation
- **WHEN** the html-report subagent finishes writing `/workspace/report.html`
- **THEN** the main agent invokes the share-html skill and includes the resulting shareable URL in its final response

#### Scenario: write_todos called before any subagent delegation
- **WHEN** the main agent starts processing a topic
- **THEN** the `write_todos` tool is called before any subagent or skill invocation

