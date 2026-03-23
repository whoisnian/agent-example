# checkpoint-persistence Specification

## Purpose
SQLite-backed LangGraph checkpointer that persists graph state keyed by `thread_id`, enabling session resume across `main.py` invocations.

## Requirements

### Requirement: Persist agent graph state to SQLite via SqliteSaver
The main agent SHALL be created with a `SqliteSaver` checkpointer backed by `checkpoints.db` in the working directory, so that all graph state (messages, intermediate node outputs) is durably persisted after each node execution.

#### Scenario: Checkpointer wired into agent
- **WHEN** `main()` calls `create_deep_agent()`
- **THEN** it passes a `SqliteSaver` instance (opened from `checkpoints.db`) as the `checkpointer` argument

#### Scenario: State persisted after first run
- **WHEN** `main()` completes a full pipeline run for a given `thread_id`
- **THEN** `checkpoints.db` contains at least one checkpoint row for that `thread_id`

### Requirement: Resume a prior thread by passing thread_id
When `main.py` is invoked with `--thread-id <uuid>`, the agent SHALL load the existing checkpoint for that thread and append the new user input to the prior message history, continuing the conversation.

#### Scenario: Resume with existing thread_id
- **WHEN** `main.py` is run with `--thread-id <existing-uuid>` and a new topic string
- **THEN** the agent receives the new `HumanMessage` appended to the prior checkpoint messages and continues the pipeline from that state

#### Scenario: New thread when thread_id is absent
- **WHEN** `main.py` is run without `--thread-id`
- **THEN** a new UUID is generated, a fresh graph state is created, and the UUID is printed to stdout before streaming begins

### Requirement: Print thread_id at the start of every run
`main()` SHALL print the active `thread_id` (generated or supplied) to stdout before any streaming output, so the user can record it for future resumption.

#### Scenario: thread_id printed on new run
- **WHEN** `main.py` starts without `--thread-id`
- **THEN** the first line of output contains the generated `thread_id` (UUID format)

#### Scenario: thread_id printed on resume
- **WHEN** `main.py` starts with `--thread-id <uuid>`
- **THEN** the first line of output contains that same `thread_id`
