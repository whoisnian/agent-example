## MODIFIED Requirements

### Requirement: Search the web for a given topic
The web-research subagent SHALL accept a topic string, perform web searches, and return a structured summary of findings including key facts, sources, and relevant context. The subagent SHALL be built using `create_agent()` from `deepagents.graph` with APIs compatible with `deepagents>=0.5.2`.

#### Scenario: Successful research
- **WHEN** the subagent is invoked with a non-empty topic string
- **THEN** it returns a structured text summary containing at least 3 distinct pieces of information about the topic

#### Scenario: Empty or vague topic
- **WHEN** the subagent is invoked with an empty or single-word topic
- **THEN** it returns whatever information it can find and includes a note that more specificity would improve results

### Requirement: Use deepseek-v3.2 via ChatTongyi
The web-research subagent SHALL be configured with `ChatTongyi(model="deepseek-v3.2")` from `langchain_community.chat_models`. The import path SHALL be valid with the latest `langchain-community` version.

#### Scenario: Model configuration
- **WHEN** the web-research subagent node is initialized
- **THEN** the underlying LLM is `ChatTongyi` with `model="deepseek-v3.2"`
