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
### 2026-03-15-optimize-agent-system-prompts
```sh
/opsx-propose Optimize the system prompts of the main agent and the subagents. The main agent should use `write_todos` tool to do planning first, and then web research, generate html report, and share html in sequence. The web research agent should focus on core keywords and search no more than three times. The html report agent should only base on the research results and do not explore the filesystem or the workspace.
# 1. The main agent should not make the planning step as step 1, the planning should be done outside of these steps.
/opsx-apply
# 1. The html report agent still return `I'll create a comprehensive HTML report about LangChain Deep Agents based on the research results provided. Let me first check the workspace and then create the report.` and call `ls /workspace` to check the workspace, which is not expected.
# 2. Maybe it's impossible to completely prohibit the html report agent from calling other tools. So expose `write_file` tool from the `FilesystemMiddleware`, and edit `create_agent()` to pass `write_file` tool to the html report agent. Then update the system prompt and `openspec/changes/optimize-agent-system-prompts/` artifacts to reflect this change.
/opsx-archive
```
### 2026-03-15-datetime-middleware
```sh
/opsx-propose Add a datetime middleware to provide task start datetime information to the agents. The main agent add `context_schema` with `start_time` field, and initialize context with `start_time` value when calling `agent.astream()`. The datetime middleware should format the datetime value into string and append it to the system prompt for the agents to use. The html report agent use the middleware provided datetime information to add a timestamp to the generated report.
# 1. Format the datetime value into unambiguous RFC3339 format like `2006-01-02T15:04:05Z07:00`.
/opsx-apply
# 1. Move the datetime middleware to a new directory `middlewares/`. And move the `DatetimeContext` dataclass as `CustomContext` in global `context.py` for future extension.
/opsx-archive
```
### 2026-03-24-add-checkpoint-persistence
```sh
/opsx:propose Add langgraph-checkpoint-sqlite to persist agent state between threads. If run main.py without thread_id argument, generate a new thread_id and start a new stream. If run with thread_id argument, load the agent state from the checkpoint and continue the stream. The new user input should be appended to the existing context messages. Add a thread_id field to the context schema.
/opsx:apply
/opsx:archive
```
