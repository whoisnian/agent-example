## Context

The pipeline currently uses `ChatTongyi` from `langchain-community` to call the DashScope API with the `deepseek-v3.2` model. The `get_model()` function in `utils.py` is the single point of model creation, used by both the main agent and all subagents. DashScope now offers an OpenAI-compatible endpoint at `https://dashscope.aliyuncs.com/compatible-mode/v1`, which allows using `ChatOpenAI` from `langchain-openai` — a better-maintained, first-party LangChain integration. The streaming loop in `main.py` currently prints event metadata but does not echo incremental token text to the console in real time.

## Goals / Non-Goals

**Goals:**
- Switch the LLM provider from `ChatTongyi` to `ChatOpenAI` using DashScope's OpenAI-compatible endpoint.
- Change the default model from `deepseek-v3.2` to `qwen3.5-flash`.
- Update `main.py` to print streamed token content incrementally (character-by-character) so the user sees output as it arrives.
- Replace `dashscope` and `langchain-community` dependencies with `langchain-openai`.

**Non-Goals:**
- Changing the agent orchestration logic, subagent structure, or system prompts.
- Adding support for multiple model providers or model selection at runtime.
- Modifying the checkpoint, middleware, or sandbox infrastructure.

## Decisions

### Decision 1: Use `ChatOpenAI` with DashScope OpenAI-compatible endpoint

**Choice**: Replace `ChatTongyi` with `ChatOpenAI(model="qwen3.5-flash", base_url="https://dashscope.aliyuncs.com/compatible-mode/v1", api_key=...)`.

**Rationale**: `langchain-openai` is a first-party LangChain package with better maintenance, broader community support, and native streaming. DashScope's OpenAI-compatible mode supports the same authentication (`DASHSCOPE_API_KEY`) and model names. This also removes the dependency on `dashscope` SDK and `langchain-community`.

**Alternative considered**: Keep `ChatTongyi` and just change the model name. Rejected because `ChatTongyi` is a community-maintained wrapper with less reliable streaming support and ties us to the `dashscope` SDK.

### Decision 2: Print incremental token content during streaming

**Choice**: In the `main.py` streaming loop, when a model node yields a message chunk with non-empty `content`, print the content immediately with `print(token.content, end="", flush=True)` to achieve real-time character streaming. Print a newline after the stream ends or on non-content events.

**Rationale**: This gives the user immediate feedback during long model calls. The existing structured event output (agent name, node, timing) is preserved for non-streaming metadata.

**Alternative considered**: Use a separate rich/curses UI for streaming output. Rejected — too complex for the current CLI-only use case.

### Decision 3: Replace `dashscope` + `langchain-community` with `langchain-openai`

**Choice**: Remove `dashscope` and `langchain-community` from `pyproject.toml`, add `langchain-openai`. Update all imports accordingly.

**Rationale**: `langchain-community` is only used for `ChatTongyi` and `DuckDuckGoSearchRun`. The DuckDuckGo tool comes from `langchain-community` as well, so we keep `langchain-community` only if needed for DuckDuckGo. After investigation, `DuckDuckGoSearchRun` is from `langchain-community`, so we must retain `langchain-community` for that. We only remove `dashscope` (the SDK) and swap `ChatTongyi` for `ChatOpenAI`.

## Risks / Trade-offs

- **[DashScope OpenAI compatibility gaps]** → DashScope's OpenAI-compatible mode may not support every OpenAI API feature. Mitigation: We only use basic chat completion and streaming, which are well-supported.
- **[Model behavior differences]** → `qwen3.5-flash` may produce different output quality than `deepseek-v3.2`. Mitigation: This is an intentional model switch; users can change the model name back if needed.
- **[Streaming output interleaved with metadata]** → Incremental token printing may interleave with structured event headers. Mitigation: Print token content inline within the existing event block, keeping metadata on separate lines.
