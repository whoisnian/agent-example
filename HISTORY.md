# openspec history

## init project
```sh
uv init agent-example
# pyproject.toml: A deep agent for various tasks

openspec init --tools github-copilot
# context: |
#   Tech stack: python, uv, asyncio, deepagents, dashscope
#   We follow PEP 8 style guide
#   Domain: a deep agent for various tasks

uv add deepagents langchain langchain-community dashscope
```

## coding with agent
Copilot Chat Agent with `Claude Sonnet 4.6`

### 2026-03-14-deepagent-research-report
```sh
/opsx-propose A general agent project based on https://github.com/langchain-ai/deepagents SDK. The main deep agent introduces two subagents to handle web research and html report generation. Use deepseek-v3.2 model from ChatTongyi in langchain_community.
# 1. The subagents should use `CompiledSubAgent` to wrap `create_agent` for simple tasks, and only used as subagents in the main deep agent, not as standalone agents. So don't create `run_web_research()` and `run_html_report()` functions.
# 2. Use dotenv.load_dotenv to load API_KEY from environment variables. Provide an .env.example file with the required environment variables and example values.
/opsx-apply
# 1. ModuleNotFoundError: No module named 'ddgs'
/opsx-archive
```
### 2026-03-14-stream-agent-messages
```sh
/opsx-propose Streaming and printing LLM messages and tool calls for better debugging and visualization of the task execution process. Use `agent.astream(stream_mode="messages", subgraphs=True, version="v2")`, and print the agent name, node type, content, token usage, and tool calls in a readable format.
# 1. Modify the openspec change name from `stream-llm-debug` to `stream-agent-messages`, and the new capabilities from `stream-debug` to `stream-agent-messages`.
/opsx-apply
# 1. EDIT(main.py): custom messages printing for agent.astream()
# 2. Update `openspec/changes/stream-agent-messages` based on the user-edited version of the current `main.py` file.
/opsx-archive
```
### 2026-03-14-docker-sandbox-provider
```sh
/opsx-propose Add DockerSandboxProvider to create DockerSandbox as backend for the main deep agent. Use python:3.14.3-bookworm as default image and /workspace as container working_dir. The sandbox needs to be passed into the subagent html-report-agent. Then html-report-agent should use write_file tool to write file into /workspace of the sandbox.
# 1. The main agent also use the sandbox as backend to execute tools in the sandbox environment. The html-report-agent need to use the same sandbox as backend, so that it can use deepagents's filesystem tools to write file into the sandbox.
# 2. The main.py should download final result html file from sandbox after task succeed.
/opsx-apply
/opsx-archive
```
### 2026-03-14-share-html-skill
```sh
/opsx-propose Add a share-html skill to the main agent. After generating the html report, the share-html skill use the `execute` tool to run curl command in sandbox to upload the file to a file sharing service and get a shareable link. The shell command could be `FILE_NAME="$(date +%Y%m%d.%H%M%S).html" && curl -s -d @/workspace/report.html "https://share.whoisnian.com:8020/api/file/workspace/${FILE_NAME}" && echo "File uploaded successfully: https://share.whoisnian.com:8020/view/workspace/${FILE_NAME}" || echo "Failed to upload file."`.
# 1. The share-html skill is implemented as Deep Agents skill, and used as `skills` argument in `create_deep_agent()`. The main agent should read the skill from the sandbox as needed. Do not include detail implementation of the share-html skill in the system prompt of the main agent.
# 2. Place the share-html skill file in the `skills/` directory of the project. The skill file should be uploaded to `/workspace/skills/` directory in the sandbox for the main agent to load and use.
/opsx-apply
# 1. Upload all files in `skills/` directory to the sandbox, so that the project skills can be easily extended.
# 2. The `sandbox.upload_files()` should not be limited to working_dir, but always upload files to absolute path in the sandbox, change to the same behavior like `sandbox.download_files()`.
/opsx-archive
```
