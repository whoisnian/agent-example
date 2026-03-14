## ADDED Requirements

### Requirement: Search the web for a given topic
The web-research subagent SHALL accept a topic string, perform web searches, and return a structured summary of findings including key facts, sources, and relevant context.

#### Scenario: Successful research
- **WHEN** the subagent is invoked with a non-empty topic string
- **THEN** it returns a structured text summary containing at least 3 distinct pieces of information about the topic

#### Scenario: Empty or vague topic
- **WHEN** the subagent is invoked with an empty or single-word topic
- **THEN** it returns whatever information it can find and includes a note that more specificity would improve results

### Requirement: Return structured research results
The web-research subagent SHALL return results as a structured string or dict that the main agent can pass directly to the html-report subagent without transformation.

#### Scenario: Result format
- **WHEN** the subagent completes its research
- **THEN** the returned value contains a `summary` field (or equivalent top-level text) that the html-report subagent can consume

### Requirement: Use deepseek-v3.2 via ChatTongyi
The web-research subagent SHALL be configured with `ChatTongyi(model="deepseek-v3.2")`.

#### Scenario: Model configuration
- **WHEN** the web-research subagent node is initialized
- **THEN** the underlying LLM is `ChatTongyi` with `model="deepseek-v3.2"`
