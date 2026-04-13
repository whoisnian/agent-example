## MODIFIED Requirements

### Requirement: Use deepseek-v3.2 via ChatTongyi
The web-research subagent SHALL be configured with `ChatOpenAI(model="qwen3.5-flash", base_url="https://dashscope.aliyuncs.com/compatible-mode/v1")` sourced from `langchain_openai`, using the `DASHSCOPE_API_KEY` environment variable for authentication.

#### Scenario: Model configuration
- **WHEN** the web-research subagent node is initialized
- **THEN** the underlying LLM is `ChatOpenAI` with `model="qwen3.5-flash"` and `base_url="https://dashscope.aliyuncs.com/compatible-mode/v1"`
