# agent-example

A deep agent that researches a topic on the web, generates a self-contained HTML report, and uploads it to a file-sharing service for instant sharing. It uses the [deepagents](https://github.com/langchain-ai/deepagents) SDK with two subagents — a web-research agent and an html-report agent — both powered by DeepSeek-V3.2 via [ChatTongyi](https://python.langchain.com/docs/integrations/chat/tongyi/) (Alibaba DashScope). All agent work runs inside a Docker sandbox container.

## Setup

**1. Install dependencies**

```bash
uv sync
```

**2. Install Docker**

A running Docker daemon is required. The agent spins up a `python:3.14.3-bookworm` container for each run.

**3. Configure environment variables**

```bash
cp .env.example .env
# Edit .env and set your DASHSCOPE_API_KEY
```

Get your DashScope API key at <https://dashscope.console.aliyun.com/>.

## Usage

```bash
uv run python main.py "your research topic here"
```

The agent will:
1. Search the web for information about the topic
2. Generate a self-contained `report.html` inside the Docker sandbox
3. Use the `share-html` skill to upload the report and print a shareable URL
4. Download `report.html` to the current directory and print its local path

## Project structure

```
main.py               # Entry point — main deep agent
agents/
  web_research.py     # Web-research CompiledSubAgent (DuckDuckGo search)
  html_report.py      # HTML-report CompiledSubAgent (writes report.html)
sandbox/
  docker_sandbox.py   # DockerSandbox backend (execute, upload, download files)
skills/
  share-html/
    SKILL.md          # DeepAgents skill: upload report.html and return a shareable URL
utils.py              # Shared helpers (model init, output formatting)
.env.example          # Required environment variables template
```

## Adding skills

Drop a new skill directory under `skills/` containing a `SKILL.md` with YAML frontmatter (`name`, `description`) and markdown instructions. It will be uploaded to the sandbox automatically on the next run and made available to the main agent via `SkillsMiddleware`.
