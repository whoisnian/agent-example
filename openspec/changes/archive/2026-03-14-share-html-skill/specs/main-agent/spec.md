## MODIFIED Requirements

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

## ADDED Requirements

### Requirement: Upload share-html skill to sandbox before agent creation
Before creating the main agent, `main()` SHALL upload the `agents/skills/share-html/SKILL.md` file to the sandbox at `/workspace/skills/project/share-html/SKILL.md` using `sandbox.upload_files()`.

#### Scenario: SKILL.md uploaded successfully
- **WHEN** `main()` creates the Docker sandbox
- **THEN** it reads `skills/share-html/SKILL.md` from the host and calls `sandbox.upload_files([("skills/share-html/SKILL.md", content)])` before calling `create_deep_agent()`

### Requirement: Wire share-html skill into main agent via skills parameter
The main agent SHALL be created with `skills=["/workspace/skills/"]` so `SkillsMiddleware` loads the share-html skill from the sandbox at runtime.

#### Scenario: SkillsMiddleware reads share-html skill
- **WHEN** `create_deep_agent()` is called with `skills=["/workspace/skills/"]`
- **THEN** the agent's context includes the share-html skill metadata and instructions read from `/workspace/skills/share-html/SKILL.md` in the sandbox

### Requirement: System prompt instructs agent to share the report
The main agent's system prompt SHALL include a step instructing the agent to use the `share-html` skill to upload and share the report after the html-report subagent completes. The system prompt SHALL NOT include the implementation details of the skill (e.g., the curl command).

#### Scenario: System prompt references share-html skill by name
- **WHEN** the main agent is initialized
- **THEN** its system prompt includes a numbered step directing it to use the share-html skill, without embedding the curl command or upload URL

#### Scenario: Share step runs after report generation
- **WHEN** the html-report subagent finishes writing `/workspace/report.html`
- **THEN** the main agent invokes the share-html skill and includes the resulting shareable URL in its final response
