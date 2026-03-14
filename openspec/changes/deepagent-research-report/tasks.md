## 1. Project Setup

- [ ] 1.1 Add `langchain-community`, `deepagents`, `dashscope`, and `python-dotenv` to `pyproject.toml` dependencies
- [ ] 1.2 Create `agents/__init__.py` to make `agents` a package
- [ ] 1.3 Create `.env.example` with all required environment variables and example values
- [ ] 1.4 Call `dotenv.load_dotenv()` at the top of `main.py` before any agent initialization
- [ ] 1.5 Add `.env` to `.gitignore` and document setup steps in README

## 2. Web Research Subagent

- [ ] 2.1 Create `agents/web_research.py` with a `build_web_research_subagent()` factory function
- [ ] 2.2 Use `create_agent` (langchain.agents) with `ChatTongyi(model="deepseek-v3.2")` and a web-search tool
- [ ] 2.3 Define the agent's system prompt to return structured research results (summary + key facts)
- [ ] 2.4 Wrap with `CompiledSubAgent` and return it — no standalone runner function

## 3. HTML Report Subagent

- [ ] 3.1 Create `agents/html_report.py` with a `build_html_report_subagent()` factory function
- [ ] 3.2 Use `create_agent` (langchain.agents) with `ChatTongyi(model="deepseek-v3.2")`
- [ ] 3.3 Define the agent's system prompt to produce self-contained HTML with inline styles and a timestamp
- [ ] 3.4 Implement a `write_report_html` tool the agent can call to write `report.html` to disk
- [ ] 3.5 Wrap with `CompiledSubAgent` and return it — no standalone runner function

## 4. Main Agent

- [ ] 4.1 Replace `main.py` with a `main()` async entry point using the deepagents SDK
- [ ] 4.2 Build the main deep agent with `create_agent` and `ChatTongyi(model="deepseek-v3.2")`
- [ ] 4.3 Register the web-research and html-report `CompiledSubAgent` instances as subagents of the main agent
- [ ] 4.4 Print the final agent response (including `report.html` path) to stdout on completion
- [ ] 4.5 Add `if __name__ == "__main__": asyncio.run(main())` guard

## 5. Validation

- [ ] 5.1 Run `python main.py` with a sample topic (e.g., "LangChain multi-agent patterns") and verify `report.html` is created
- [ ] 5.2 Open `report.html` in a browser and confirm it renders without external dependencies
- [ ] 5.3 Verify correct model (`deepseek-v3.2`) is used by checking DashScope logs or request traces
