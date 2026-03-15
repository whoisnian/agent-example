## ADDED Requirements

### Requirement: Generate report solely from provided research results
The html-report subagent's system prompt SHALL explicitly instruct the agent to use only the research results passed to it as input. The agent SHALL NOT use `read_file`, `glob`, `grep`, or any other read/explore tool. Its only permitted tool call is `write_file` to write the generated HTML to `/workspace/report.html`.

#### Scenario: No filesystem exploration performed
- **WHEN** the html-report subagent is invoked with research results
- **THEN** it issues no calls to `read_file`, `glob`, `grep`, or any other tool except `write_file`

#### Scenario: Report content derived from input only
- **WHEN** the report is generated
- **THEN** all content (title, summary, facts) is derived exclusively from the research results passed in as input, not from any file read from the sandbox or workspace

#### Scenario: write_file is the sole tool call
- **WHEN** the subagent completes report generation
- **THEN** it has made exactly one tool call: `write_file` with path `/workspace/report.html` and the complete HTML content
