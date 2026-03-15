## ADDED Requirements

### Requirement: Use write_todos to plan pipeline before execution
The main agent's system prompt SHALL instruct it to call the `write_todos` tool as a preliminary planning step — outside of and before the numbered pipeline steps — listing all planned actions (web research, html report generation, share html, download report) before invoking any subagent or skill.

#### Scenario: Planning step precedes pipeline execution
- **WHEN** the main agent receives a research topic
- **THEN** its first action is a `write_todos` call enumerating the pipeline steps, and only afterward does it delegate to the web-research subagent

#### Scenario: Execution follows the planned sequence
- **WHEN** the main agent has called `write_todos`
- **THEN** it executes steps in the declared order: web-research subagent → html-report subagent → share-html skill → report path returned to user, without reordering or skipping steps

## MODIFIED Requirements

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
