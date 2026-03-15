## MODIFIED Requirements

### Requirement: Accept research topic and run pipeline
The main agent SHALL accept a user-supplied research topic string, create a `DockerSandbox` via `DockerSandboxProvider`, upload the share-html `SKILL.md` to the sandbox before creating the agent, record `start_time = datetime.now()` at pipeline start, pass `context_schema=DatetimeContext` to `create_deep_agent()`, invoke the web-research subagent to gather information via streaming, pass the results and the sandbox to the html-report subagent, use the share-html skill to upload and share the report, print the path of the generated HTML report and the shareable URL to the caller after streaming completes, and stop the sandbox in a `finally` block.

#### Scenario: Successful end-to-end run
- **WHEN** the user calls `main()` (or the CLI entry point) with a non-empty topic string
- **THEN** a Docker sandbox is created, the share-html `SKILL.md` is uploaded to the sandbox, `start_time` is captured via `datetime.now()`, the pipeline runs (web-research → html-report → share-html), intermediate chunks are streamed to stdout, `report.html` is downloaded from the sandbox to the host, the local path to the downloaded file is printed, the shareable URL is printed, and the sandbox is stopped on completion

#### Scenario: Missing API key
- **WHEN** `DASHSCOPE_API_KEY` environment variable is not set
- **THEN** the agent raises a clear error before making any API call

#### Scenario: Pipeline error triggers sandbox cleanup
- **WHEN** the pipeline raises an exception after the sandbox is created
- **THEN** `sandbox.stop()` is still called and the container is removed

## ADDED Requirements

### Requirement: Declare DatetimeContext as context_schema and pass start_time to astream
`main()` SHALL pass `context_schema=DatetimeContext` to `create_deep_agent()` and supply `context=DatetimeContext(start_time=start_time)` in the keyword arguments to `agent.astream()` so that all middleware in the pipeline can access the typed context.

#### Scenario: start_time propagated to middleware
- **WHEN** `agent.astream()` is called with `context=DatetimeContext(start_time=start_time)`
- **THEN** `request.runtime.context.start_time` inside any middleware equals the `start_time` value recorded in `main()`

#### Scenario: context_schema declared on agent
- **WHEN** `create_deep_agent()` is called with `context_schema=DatetimeContext`
- **THEN** the compiled graph accepts a `DatetimeContext` instance as the runtime context without type errors
