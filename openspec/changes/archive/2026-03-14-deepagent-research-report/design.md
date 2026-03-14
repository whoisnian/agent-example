## Context

The repository currently has a stub `main.py` with no real functionality. The goal is to implement a multi-agent pipeline using the [deepagents](https://github.com/langchain-ai/deepagents) SDK. The main agent accepts a research topic from the user, delegates to a web-research subagent, then passes those results to an html-report subagent to produce a final deliverable.

All agents use `deepseek-v3.2` accessed via `ChatTongyi` from `langchain_community.chat_models`, which in turn uses Alibaba DashScope as the backend.

## Goals / Non-Goals

**Goals:**

- Implement a functioning three-agent pipeline (main + web-research + html-report)
- Use `deepseek-v3.2` via `ChatTongyi` for all agents
- Keep the project structure simple: a flat `agents/` package alongside `main.py`
- Provide clear configuration via environment variable (`DASHSCOPE_API_KEY`)

**Non-Goals:**

- Persistent storage of research or reports across runs
- A web UI or REST API layer
- Support for models other than `deepseek-v3.2` at this stage
- Streaming output to the user

## Decisions

### 1. Agent framework: deepagents SDK

**Decision**: Use the `deepagents` SDK to define the main agent and its subagents.

**Rationale**: The user explicitly requested the deepagents SDK. It provides a declarative way to define agent graphs with tool-calling subagents, reducing boilerplate compared to raw LangChain agent loops.

**Alternative considered**: Plain LangChain `AgentExecutor` — rejected because it doesn't offer the same structured multi-agent delegation pattern.

### 2. Model: ChatTongyi with deepseek-v3.2

**Decision**: Configure all agents with `ChatTongyi(model="deepseek-v3.2")`.

**Rationale**: User requirement. `ChatTongyi` is the standard LangChain community wrapper for Alibaba DashScope models; deepseek-v3.2 is the target model.

**Alternative considered**: OpenAI-compatible endpoint — more portable but diverges from the requested stack.

### 3. Project layout: flat `agents/` package

**Decision**: Place subagent modules in `agents/web_research.py` and `agents/html_report.py`, with `main.py` as the top-level runner.

**Rationale**: Minimal structure for an example project. No need for a `src/` layout when the package is not distributed.

**Alternative considered**: Single-file implementation in `main.py` — rejected because it makes it harder to test subagents in isolation.

### 4. HTML report output: write to file

**Decision**: The html-report subagent writes the report to `report.html` in the current working directory and returns the path.

**Rationale**: Simple, portable, and verifiable. The main agent surfaces the path to the user.

**Alternative considered**: Return HTML as a string — less user-friendly for a demo project.

## Risks / Trade-offs

- **DashScope API availability** → Mitigation: document `DASHSCOPE_API_KEY` requirement clearly in README; fail fast with a clear error if the key is missing.
- **Web search tool availability in deepagents** → Mitigation: confirm the SDK's built-in search tool interface during implementation; if not available, use a simple `requests`-based DuckDuckGo scrape as fallback.
- **deepseek-v3.2 model name accuracy** → Mitigation: verify the exact model identifier string against DashScope docs at implementation time; allow override via environment variable.
- **Non-deterministic HTML output** → Accepted trade-off for an LLM-generated report; no strict schema enforcement on the HTML.
