## Why

The current system prompts lack structured guidance: the main agent has no explicit planning step, the web-research agent performs unbounded searches, and the html-report agent is not restricted from exploring the filesystem. This leads to unpredictable behavior, wasted API calls, and unreliable report generation.

## What Changes

- **Main agent system prompt**: Add an explicit first step instructing the agent to call `write_todos` to plan all steps before executing the pipeline (web research → html report → share html).
- **Web-research agent system prompt**: Constrain the agent to focus on core keywords extracted from the topic and perform no more than three DuckDuckGo searches.
- **HTML-report agent system prompt**: Restrict the agent to use only the provided research results as its source; explicitly prohibit filesystem exploration, web browsing, or reading from the workspace.

## Capabilities

### New Capabilities

<!-- None -->

### Modified Capabilities

- `main-agent`: System prompt requirement changes — must instruct the agent to call `write_todos` for planning before delegating to subagents, and then execute web research, html report generation, and share-html in strict sequence.
- `web-research-agent`: System prompt requirement changes — must constrain searches to core keywords derived from the topic, with a hard limit of three searches.
- `html-report-agent`: System prompt requirement changes — must explicitly forbid filesystem exploration and web browsing; report must be generated solely from the research results passed in as input.

## Impact

- `main.py` (`_SYSTEM_PROMPT`): update system prompt text
- `agents/web_research.py` (`_SYSTEM_PROMPT`): update system prompt text
- `agents/html_report.py` (`_SYSTEM_PROMPT`): update system prompt text
- `openspec/specs/main-agent/spec.md`: delta for planning-first requirement
- `openspec/specs/web-research-agent/spec.md`: delta for search-limit requirement
- `openspec/specs/html-report-agent/spec.md`: delta for research-results-only requirement
