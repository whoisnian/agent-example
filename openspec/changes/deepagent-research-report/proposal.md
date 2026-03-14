## Why

This project needs a working agent implementation to demonstrate the deepagents SDK. Currently `main.py` is a placeholder. By building a main deep agent that orchestrates two specialized subagents — one for web research and one for HTML report generation — we deliver an end-to-end example of multi-agent collaboration using the deepagents framework with DeepSeek-V3.2 via ChatTongyi.

## What Changes

- Implement `main.py` as the entry-point that initializes and runs the deep agent pipeline
- Add a **web-research** subagent that performs web searches and gathers structured information on a given topic
- Add an **html-report** subagent that takes research results and renders a formatted HTML report
- Wire the main agent to delegate research to the web-research subagent, then pass results to the html-report subagent
- Configure all agents to use the `deepseek-v3.2` model via `ChatTongyi` from `langchain_community.chat_models`
- Add project dependencies (`langchain-community`, `deepagents`, `dashscope`) to `pyproject.toml`

## Capabilities

### New Capabilities

- `main-agent`: Top-level deep agent that orchestrates the full pipeline — accepts a user query, delegates web research and report generation to subagents, and returns the final HTML report path
- `web-research-agent`: Subagent responsible for searching the web and returning structured research results for a given topic
- `html-report-agent`: Subagent responsible for accepting structured research data and producing a self-contained HTML report file

### Modified Capabilities

<!-- No existing specs to modify -->

## Impact

- `main.py`: Replaced with full agent implementation
- `pyproject.toml`: New runtime dependencies added (`langchain-community`, `deepagents`, `dashscope`)
- New files: `agents/web_research.py`, `agents/html_report.py`, `agents/__init__.py`
- External dependency: Alibaba DashScope API key required at runtime (`DASHSCOPE_API_KEY` env var)
