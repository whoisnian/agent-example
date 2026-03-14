import asyncio
import os
import sys

from dotenv import load_dotenv
from langchain_community.chat_models import ChatTongyi

from deepagents.graph import create_deep_agent

from agents.html_report import build_html_report_subagent
from agents.web_research import build_web_research_subagent

load_dotenv()

_SYSTEM_PROMPT = """You are a research orchestrator. Given a topic from the user:
1. Use the web-research subagent to gather information about the topic.
2. Pass the full research results to the html-report subagent to generate an HTML report.
3. Report the path of the generated HTML file back to the user."""


async def main() -> None:
    api_key = os.environ.get("DASHSCOPE_API_KEY")
    if not api_key:
        print(
            "Error: DASHSCOPE_API_KEY is not set.\n"
            "Copy .env.example to .env and add your DashScope API key.",
            file=sys.stderr,
        )
        sys.exit(1)

    topic = " ".join(sys.argv[1:]) or "LangChain multi-agent patterns"
    print(f"Researching: {topic}\n")

    model = ChatTongyi(model_name="deepseek-v3.2")
    agent = create_deep_agent(
        model,
        system_prompt=_SYSTEM_PROMPT,
        subagents=[
            build_web_research_subagent(),
            build_html_report_subagent(),
        ],
    )

    result = await agent.ainvoke(
        {"messages": [{"role": "user", "content": f"Research this topic and generate an HTML report: {topic}"}]}
    )
    print(result["messages"][-1].content)


if __name__ == "__main__":
    asyncio.run(main())
