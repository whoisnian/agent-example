## 1. Main Agent System Prompt

- [x] 1.1 Update `_SYSTEM_PROMPT` in `main.py` to instruct `write_todos` as a preliminary planning action before the numbered pipeline steps
- [x] 1.2 Ensure the numbered pipeline steps are: 1. web research → 2. html report → 3. share html → 4. report path (write_todos is outside this numbered list)
- [x] 1.3 Verify the prompt does not include share-html implementation details (no curl command or upload URL)

## 2. Web Research Agent System Prompt

- [x] 2.1 Update `_SYSTEM_PROMPT` in `agents/web_research.py` to instruct keyword extraction (1–3 core keywords) before searching
- [x] 2.2 Add explicit instruction to perform at most three DuckDuckGo searches total
- [x] 2.3 Verify the prompt does not encourage open-ended or iterative searching

## 3. HTML Report Agent — Tool Restriction

- [x] 3.1 In `build_html_report_subagent()`, instantiate `FilesystemMiddleware(backend=sandbox)` and extract the `write_file` tool by name from `fs.tools`
- [x] 3.2 Pass `tools=[write_file_tool]` to `create_agent()` instead of `middleware=[FilesystemMiddleware(...)]`
- [x] 3.3 Update `_SYSTEM_PROMPT` in `agents/html_report.py` to remove prompt-level prohibitions (enforcement is now structural)

## 4. Validation

- [x] 4.1 Run `uv run python main.py "<topic>"` end-to-end and confirm `write_todos` is called first in the streamed output
- [x] 4.2 Confirm the web-research agent issues no more than three search tool calls in the stream output
- [x] 4.3 Confirm the html-report agent issues only a single `write_file` tool call and no read/explore calls
