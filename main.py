import asyncio
import sys

from deepagents.graph import create_deep_agent

from agents.html_report import build_html_report_subagent
from agents.web_research import build_web_research_subagent
from utils import get_model

_SYSTEM_PROMPT = """You are a research orchestrator. Given a topic from the user:
1. Use the web-research subagent to gather information about the topic.
2. Pass the full research results to the html-report subagent to generate an HTML report.
3. Report the path of the generated HTML file back to the user."""


async def main() -> None:
    model = get_model()

    topic = " ".join(sys.argv[1:]) or "What's LangChain Deep Agents?"
    print(f"Researching: {topic}")

    agent = create_deep_agent(
        name="main-agent",
        model=model,
        system_prompt=_SYSTEM_PROMPT,
        subagents=[
            build_web_research_subagent(),
            build_html_report_subagent(),
        ],
    )

    result = await agent.ainvoke(
        {"messages": [{"role": "user", "content": topic}]}
    )
    print(result["messages"][-1].content)


if __name__ == "__main__":
    asyncio.run(main())
