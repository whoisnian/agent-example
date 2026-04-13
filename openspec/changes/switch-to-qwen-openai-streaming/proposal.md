## Why

The current pipeline uses `ChatTongyi` from `langchain-community` with the `deepseek-v3.2` model. We want to switch to `qwen3.5-flash` for better performance and cost efficiency, and invoke the DashScope API through its OpenAI-compatible endpoint using `ChatOpenAI` from `langchain-openai`. This also enables native streaming support and aligns with the broader industry move toward OpenAI-compatible APIs. Additionally, `main.py` should print streamed token content in real time so the user can see incremental output.

## What Changes

- Replace `ChatTongyi` with `ChatOpenAI` configured to use DashScope's OpenAI-compatible base URL (`https://dashscope.aliyuncs.com/compatible-mode/v1`).
- Change the default model from `deepseek-v3.2` to `qwen3.5-flash`.
- Replace the `dashscope` and `langchain-community` dependencies with `langchain-openai`.
- Update `main.py` to print streamed token text chunks incrementally (character-by-character streaming output to the console).

## Capabilities

### New Capabilities

### Modified Capabilities
- `main-agent`: Update model instantiation to use `ChatOpenAI` with DashScope OpenAI-compatible endpoint and `qwen3.5-flash`.
- `web-research-agent`: Update model reference from `deepseek-v3.2` to `qwen3.5-flash` via shared `get_model()`.
- `stream-agent-messages`: Add real-time incremental token streaming to console output.
- `dependency-upgrade`: Replace `dashscope`/`langchain-community` with `langchain-openai` in project dependencies.

## Impact

- **Code**: `utils.py` (`get_model()`), `main.py` (streaming print logic), `pyproject.toml` (dependency swap).
- **Dependencies**: Remove `dashscope`, `langchain-community`; add `langchain-openai`.
- **Environment**: `DASHSCOPE_API_KEY` remains the auth mechanism (passed as `api_key` to `ChatOpenAI`).
- **Specs**: Four existing specs updated to reflect the new model, provider, and streaming behavior.
