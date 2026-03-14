# agent-example

A deep agent that researches a topic on the web and generates a self-contained HTML report. It uses the [deepagents](https://github.com/langchain-ai/deepagents) SDK with two subagents — a web-research agent and an html-report agent — both powered by DeepSeek-V3.2 via [ChatTongyi](https://python.langchain.com/docs/integrations/chat/tongyi/) (Alibaba DashScope).

## Setup

**1. Install dependencies**

```bash
uv sync
```

**2. Configure environment variables**

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
2. Generate a self-contained `report.html` in the current directory
3. Print the report path on completion

## Project structure

```
main.py               # Entry point — main deep agent
agents/
  web_research.py     # Web-research CompiledSubAgent (DuckDuckGo search)
  html_report.py      # HTML-report CompiledSubAgent (writes report.html)
.env.example          # Required environment variables template
```
