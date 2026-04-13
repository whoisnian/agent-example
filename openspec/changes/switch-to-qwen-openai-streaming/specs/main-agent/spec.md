## MODIFIED Requirements

### Requirement: Use deepseek-v3.2 via ChatTongyi
All agents in the pipeline SHALL be configured with `ChatOpenAI(model="qwen3.5-flash", base_url="https://dashscope.aliyuncs.com/compatible-mode/v1")` sourced from `langchain_openai`. The API key SHALL be read from the `DASHSCOPE_API_KEY` environment variable and passed as the `api_key` parameter.

#### Scenario: Model configuration
- **WHEN** any agent node is initialized
- **THEN** the underlying LLM instance is `ChatOpenAI` with `model="qwen3.5-flash"` and `base_url="https://dashscope.aliyuncs.com/compatible-mode/v1"`

#### Scenario: Missing API key
- **WHEN** `DASHSCOPE_API_KEY` environment variable is not set
- **THEN** `get_model()` SHALL raise a `ValueError` before making any API call
