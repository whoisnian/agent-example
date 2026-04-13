## ADDED Requirements

### Requirement: Add filesystem permissions to main agent
The main agent SHALL be created with `permissions` rules that restrict write operations to `/workspace/` within the sandbox, providing defense-in-depth alongside the Docker isolation.

#### Scenario: Write operations restricted to /workspace/
- **WHEN** `create_deep_agent()` is called for the main agent
- **THEN** a `permissions` argument is passed that allows read/write under `/workspace/**` and denies writes outside it

#### Scenario: Read operations remain unrestricted within sandbox
- **WHEN** the main agent attempts to read files anywhere in the sandbox
- **THEN** the read is allowed by the permissions rules

## MODIFIED Requirements

### Requirement: Accept research topic and run pipeline
The main agent SHALL accept an optional `--thread-id` flag and a positional research topic string (joined from remaining `sys.argv` arguments). If `--thread-id` is not supplied, generate a new UUID. Print the active `thread_id` to stdout before streaming begins. Create a `DockerSandbox` via `DockerSandboxProvider`, upload the share-html `SKILL.md` to the sandbox before creating the agent, record `start_time = datetime.now()` at pipeline start, pass `context_schema=CustomContext` and `checkpointer=SqliteSaver(checkpoints.db)` to `create_deep_agent()`, pass `config={"configurable": {"thread_id": thread_id}}` and `context=CustomContext(start_time=start_time, thread_id=thread_id)` to `agent.astream()`, invoke the web-research subagent to gather information via streaming, pass the results and the sandbox to the html-report subagent, use the share-html skill to upload and share the report, print the path of the generated HTML report and the shareable URL to the caller after streaming completes, and stop the sandbox in a `finally` block. All `create_deep_agent()` and `create_agent()` calls SHALL use APIs compatible with `deepagents>=0.5.2`.

#### Scenario: Successful end-to-end run (new thread)
- **WHEN** the user calls `main()` without `--thread-id`
- **THEN** a UUID thread_id is generated and printed, a Docker sandbox is created, the pipeline runs (web-research → html-report → share-html), `report.html` is downloaded to the host, the local path and shareable URL are printed, and the sandbox is stopped on completion

#### Scenario: Successful resume (existing thread)
- **WHEN** the user calls `main()` with `--thread-id <uuid>` and a new topic string
- **THEN** the existing checkpoint is loaded, the new user message is appended, the pipeline continues from the checkpointed state, and the sandbox is stopped on completion

#### Scenario: Missing API key
- **WHEN** `DASHSCOPE_API_KEY` environment variable is not set
- **THEN** the agent raises a clear error before making any API call

#### Scenario: Pipeline error triggers sandbox cleanup
- **WHEN** the pipeline raises an exception after the sandbox is created
- **THEN** `sandbox.stop()` is still called and the container is removed
