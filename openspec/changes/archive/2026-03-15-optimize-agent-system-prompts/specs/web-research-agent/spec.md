## ADDED Requirements

### Requirement: Focus searches on core keywords with a maximum of three queries
The web-research subagent's system prompt SHALL instruct it to first extract 1–3 core keywords from the given topic, then perform at most three targeted DuckDuckGo searches using those keywords — no more, regardless of how broad the topic is.

#### Scenario: Keyword extraction before searching
- **WHEN** the subagent receives a topic
- **THEN** it identifies 1–3 core keywords from the topic before issuing any search query

#### Scenario: Search count does not exceed three
- **WHEN** the subagent performs research
- **THEN** it issues at most three calls to the DuckDuckGo search tool in total

#### Scenario: Searches use core keywords
- **WHEN** the subagent formulates search queries
- **THEN** each query is based on the core keywords extracted from the topic, not the full topic string verbatim
