## Context

The pipeline consists of three agents: the main orchestrator (`main-agent`), the web researcher (`web-research-agent`), and the report generator (`html-report-agent`). Their current system prompts provide the right end goals but lack structural guardrails:

- The main agent has no planning step, so it may jump straight into tool calls without sequencing.
- The web-research agent has no cap on how many searches to perform, leading to excess API calls and latency.
- The html-report agent is not prohibited from exploring the filesystem or workspace, which risks it consuming stale or irrelevant data from prior runs.

All changes are prompt-only: no new tools, middleware, or dependencies are required.

## Goals / Non-Goals

**Goals:**
- Add an explicit `write_todos` planning step to the main agent system prompt so it always sequences: plan → web research → html report → share html.
- Constrain the web-research agent to extract core keywords from the topic and cap searches at three.
- Prohibit the html-report agent from filesystem exploration; it must derive all content exclusively from the research results passed to it.

**Non-Goals:**
- Changing the model, middleware, tools, or agent graph structure.
- Altering the output format of any agent.
- Adding retry logic or error handling.

## Decisions

### Decision: Prompt-only change, no code structure changes
All three changes touch only the `_SYSTEM_PROMPT` string constants in `main.py`, `agents/web_research.py`, and `agents/html_report.py`. No agent builder signatures, graph wiring, or middleware configurations are modified.

**Rationale**: The problems are behavioral, not structural. Prompt engineering is the minimal, reversible fix. Modifying code structure would be over-engineering for what is purely a guidance issue.

**Alternative considered**: Adding a `max_iterations` limiter to the web-research agent via a custom tool wrapper. Rejected — prompt-level guidance is sufficient and avoids unnecessary complexity.

### Decision: Use `write_todos` as the planning vehicle for the main agent
The main agent's prompt will instruct it to call `write_todos` as step 0, explicitly listing all planned steps before taking any action.

**Rationale**: `write_todos` is already available (shown in `main.py` event streaming). Making planning observable via an existing tool adds no new dependency while making the agent's intent transparent in the streamed output.

### Decision: Keyword extraction as a prompt instruction for web research
The web-research agent prompt will instruct the agent to first identify 1–3 core keywords from the topic, then plan and perform at most three targeted searches using those keywords.

**Rationale**: DuckDuckGo searches are unmetered but slow; limiting to three forces the agent to be selective. Keyword extraction as a prompt step mirrors chain-of-thought decomposition without needing a separate tool.

### Decision: Explicit filesystem prohibition for html-report agent
The html-report agent's prompt will contain a clear prohibition: "Do NOT use read_file, glob, grep, or any other tool — your only tool call should be write_file."

**Rationale**: `FilesystemMiddleware` exposes read tools alongside write tools. Without an explicit prohibition, the agent may attempt to read previous reports or explore `/workspace`, introducing non-determinism.

## Risks / Trade-offs

- [Risk] LLM may ignore the `write_todos` planning step under token pressure → Mitigation: State it as step 1 in a numbered list; numbered lists are harder to skip than prose instructions.
- [Risk] Hard-capping at three searches may produce insufficient results for broad topics → Mitigation: The prompt instructs the agent to choose the three most impactful queries; acceptable trade-off for cost/latency reduction.
- [Risk] Filesystem prohibition may cause the html-report agent to fail if it has already partially written a file and tries to verify it → Mitigation: The prohibition is on read/explore tools, not on `write_file` itself; the current prompt already says to call `write_file` and return.
