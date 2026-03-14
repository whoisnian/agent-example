## MODIFIED Requirements

### Requirement: Accept research topic and run pipeline
The main agent SHALL accept a user-supplied research topic string, invoke the web-research subagent to gather information via streaming, pass the results to the html-report subagent, and print the path of the generated HTML report to the caller after streaming completes.

#### Scenario: Successful end-to-end run
- **WHEN** the user calls `main()` (or the CLI entry point) with a non-empty topic string
- **THEN** the pipeline streams intermediate chunks to stdout and, after all chunks are consumed, prints the path to the generated `report.html`

#### Scenario: Missing API key
- **WHEN** `DASHSCOPE_API_KEY` environment variable is not set
- **THEN** the agent raises a clear error before making any API call
