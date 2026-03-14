# html-report-agent Specification

## Purpose
TBD - created by archiving change deepagent-research-report. Update Purpose after archive.
## Requirements
### Requirement: Generate an HTML report from research results
The html-report subagent SHALL accept structured research results and produce a self-contained HTML file (`report.html`) in the current working directory.

#### Scenario: Successful report generation
- **WHEN** the subagent is invoked with non-empty research results
- **THEN** it writes a valid HTML file to `report.html` and returns the absolute path of that file

#### Scenario: Report content completeness
- **WHEN** the report is generated
- **THEN** it includes a title derived from the research topic, the research summary, and a timestamp

### Requirement: Report is self-contained HTML
The generated report SHALL be a single standalone HTML file with all styling inlined (no external CSS or JS dependencies).

#### Scenario: Self-contained file
- **WHEN** `report.html` is opened in a browser without internet access
- **THEN** it renders correctly without any missing resources

### Requirement: Overwrite existing report
The html-report subagent SHALL overwrite `report.html` if it already exists, without prompting.

#### Scenario: Overwrite on re-run
- **WHEN** `report.html` already exists in the working directory
- **THEN** the subagent replaces it with the newly generated report

### Requirement: Use deepseek-v3.2 via ChatTongyi
The html-report subagent SHALL be configured with `ChatTongyi(model="deepseek-v3.2")`.

#### Scenario: Model configuration
- **WHEN** the html-report subagent node is initialized
- **THEN** the underlying LLM is `ChatTongyi` with `model="deepseek-v3.2"`

