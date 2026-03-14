## ADDED Requirements

### Requirement: Accept research topic and run pipeline
The main agent SHALL accept a user-supplied research topic string, invoke the web-research subagent to gather information, pass the results to the html-report subagent, and return the path of the generated HTML report to the caller.

#### Scenario: Successful end-to-end run
- **WHEN** the user calls `main()` (or the CLI entry point) with a non-empty topic string
- **THEN** the pipeline completes and prints the path to the generated `report.html`

#### Scenario: Missing API key
- **WHEN** `DASHSCOPE_API_KEY` environment variable is not set
- **THEN** the agent raises a clear error before making any API call

### Requirement: Use deepseek-v3.2 via ChatTongyi
All agents in the pipeline SHALL be configured with `ChatTongyi(model="deepseek-v3.2")` sourced from `langchain_community.chat_models`.

#### Scenario: Model configuration
- **WHEN** any agent node is initialized
- **THEN** the underlying LLM instance is `ChatTongyi` with `model="deepseek-v3.2"`

### Requirement: Delegate to subagents
The main agent SHALL delegate web research to the `web-research` subagent and report generation to the `html-report` subagent rather than performing these tasks itself.

#### Scenario: Delegation to web-research subagent
- **WHEN** the main agent receives a topic
- **THEN** it calls the web-research subagent with that topic and waits for structured results

#### Scenario: Delegation to html-report subagent
- **WHEN** the main agent receives research results
- **THEN** it calls the html-report subagent with those results and waits for the report path
