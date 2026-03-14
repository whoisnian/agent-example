## 1. Project Setup

- [x] 1.1 Add `langchain-community`, `deepagents`, `dashscope`, and `python-dotenv` to `pyproject.toml` dependencies
- [x] 1.2 Create `agents/__init__.py` to make `agents` a package
- [x] 1.3 Create `.env.example` with all required environment variables and example values
- [x] 1.4 Call `dotenv.load_dotenv()` at the top of `main.py` before any agent initialization
- [x] 1.5 Add `.env` to `.gitignore` and document setup steps in README

## 2. Web Research Subagent

- [x] 2.1 Create `agents/web_research.py` with a `build_web_research_subagent()` factory function
- [x] 2.2 Use `create_agent` (langchain.agents) with `ChatTongyi(model="deepseek-v3.2")` and a web-search tool
- [x] 2.3 Define the agent's system prompt to return structured research results (summary + key facts)
- [x] 2.4 Wrap with `CompiledSubAgent` and return it — no standalone runner function

## 3. HTML Report Subagent

- [x] 3.1 Create `agents/html_report.py` with a `build_html_report_subagent()` factory function
- [x] 3.2 Use `create_agent` (langchain.agents) with `ChatTongyi(model="deepseek-v3.2")`
- [x] 3.3 Define the agent's system prompt to produce self-contained HTML with inline styles and a timestamp
- [x] 3.4 Implement a `write_report_html` tool the agent can call to write `report.html` to disk
- [x] 3.5 Wrap with `CompiledSubAgent` and return it — no standalone runner function

## 4. Main Agent

- [x] 4.1 Replace `main.py` with a `main()` async entry point using the deepagents SDK
- [x] 4.2 Build the main deep agent with `create_agent` and `ChatTongyi(model="deepseek-v3.2")`
- [x] 4.3 Register the web-research and html-report `CompiledSubAgent` instances as subagents of the main agent
- [x] 4.4 Print the final agent response (including `report.html` path) to stdout on completion
- [x] 4.5 Add `if __name__ == "__main__": asyncio.run(main())` guard

## 5. Validation

- [x] 5.1 Run `python main.py` with a sample topic (e.g., "LangChain multi-agent patterns") and verify `report.html` is created
- [x] 5.2 Open `report.html` in a browser and confirm it renders without external dependencies
- [x] 5.3 Verify correct model (`deepseek-v3.2`) is used by checking DashScope logs or request traces
