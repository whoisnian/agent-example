## 1. Dependency Updates

- [ ] 1.1 Remove `dashscope` from `pyproject.toml` dependencies
- [ ] 1.2 Add `langchain-openai` to `pyproject.toml` dependencies
- [ ] 1.3 Run `uv sync` and verify all dependencies resolve without conflicts

## 2. Model Provider Switch

- [ ] 2.1 Update `utils.py`: replace `ChatTongyi` import with `ChatOpenAI` from `langchain_openai`, set `base_url` to DashScope OpenAI-compatible endpoint, change model to `qwen3.5-flash`
- [ ] 2.2 Remove unused `from langchain_community.chat_models import ChatTongyi` import

## 3. Streaming Output

- [ ] 3.1 Update `main.py` streaming loop: add `print(token.content, end="", flush=True)` for model node chunks with non-empty content to enable real-time incremental token output
