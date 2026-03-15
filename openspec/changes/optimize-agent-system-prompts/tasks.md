## 1. Main Agent System Prompt

- [ ] 1.1 Update `_SYSTEM_PROMPT` in `main.py` to instruct `write_todos` as a preliminary planning action before the numbered pipeline steps
- [ ] 1.2 Ensure the numbered pipeline steps are: 1. web research → 2. html report → 3. share html → 4. report path (write_todos is outside this numbered list)
- [ ] 1.3 Verify the prompt does not include share-html implementation details (no curl command or upload URL)

## 2. Web Research Agent System Prompt

- [ ] 2.1 Update `_SYSTEM_PROMPT` in `agents/web_research.py` to instruct keyword extraction (1–3 core keywords) before searching
- [ ] 2.2 Add explicit instruction to perform at most three DuckDuckGo searches total
- [ ] 2.3 Verify the prompt does not encourage open-ended or iterative searching

## 3. HTML Report Agent System Prompt

- [ ] 3.1 Update `_SYSTEM_PROMPT` in `agents/html_report.py` to instruct the agent to use only the provided research results
- [ ] 3.2 Add explicit prohibition: do NOT call `read_file`, `glob`, `grep`, or any other tool except `write_file`
- [ ] 3.3 Verify the prompt clearly states `write_file` is the only permitted tool call

## 4. Validation

- [ ] 4.1 Run `uv run python main.py "<topic>"` end-to-end and confirm `write_todos` is called first in the streamed output
- [ ] 4.2 Confirm the web-research agent issues no more than three search tool calls in the stream output
- [ ] 4.3 Confirm the html-report agent issues only a single `write_file` tool call and no read/explore calls
