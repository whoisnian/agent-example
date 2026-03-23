## MODIFIED Requirements

### Requirement: Accept research topic and run pipeline
The main agent SHALL accept an optional `--thread-id` flag and a positional research topic string (joined from remaining `sys.argv` arguments). If `--thread-id` is not supplied, generate a new UUID. Print the active `thread_id` to stdout before streaming begins. Create a `DockerSandbox` via `DockerSandboxProvider`, upload the share-html `SKILL.md` to the sandbox before creating the agent, record `start_time = datetime.now()` at pipeline start, pass `context_schema=CustomContext` and `checkpointer=SqliteSaver(checkpoints.db)` to `create_deep_agent()`, pass `config={"configurable": {"thread_id": thread_id}}` and `context=CustomContext(start_time=start_time, thread_id=thread_id)` to `agent.astream()`, invoke the web-research subagent to gather information via streaming, pass the results and the sandbox to the html-report subagent, use the share-html skill to upload and share the report, print the path of the generated HTML report and the shareable URL to the caller after streaming completes, and stop the sandbox in a `finally` block.

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

### Requirement: Declare CustomContext as context_schema and pass start_time and thread_id to astream
`main()` SHALL pass `context_schema=CustomContext` to `create_deep_agent()` and supply `context=CustomContext(start_time=start_time, thread_id=thread_id)` in the keyword arguments to `agent.astream()` so that all middleware in the pipeline can access the typed context including the active thread identifier.

#### Scenario: start_time and thread_id propagated to middleware
- **WHEN** `agent.astream()` is called with `context=CustomContext(start_time=start_time, thread_id=thread_id)`
- **THEN** `request.runtime.context.start_time` and `request.runtime.context.thread_id` inside any middleware equal the values recorded in `main()`

#### Scenario: context_schema declared on agent
- **WHEN** `create_deep_agent()` is called with `context_schema=CustomContext`
- **THEN** the compiled graph accepts a `CustomContext` instance as the runtime context without type errors

### Requirement: Wire SqliteSaver checkpointer into main agent
`main()` SHALL open a `SqliteSaver` from `checkpoints.db` using its context manager and pass it as `checkpointer` to `create_deep_agent()`, and pass `{"configurable": {"thread_id": thread_id}}` as the `config` argument to `agent.astream()`.

#### Scenario: Checkpointer passed to create_deep_agent
- **WHEN** `create_deep_agent()` is called
- **THEN** the `checkpointer` parameter is a `SqliteSaver` instance backed by `checkpoints.db`

#### Scenario: thread_id in astream config
- **WHEN** `agent.astream()` is called
- **THEN** the `config` argument contains `{"configurable": {"thread_id": thread_id}}`
