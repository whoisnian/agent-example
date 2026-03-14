from langchain_community.chat_models import ChatTongyi
from langchain_community.tools import DuckDuckGoSearchRun

from deepagents.graph import create_agent
from deepagents.middleware.subagents import CompiledSubAgent

_SYSTEM_PROMPT = """You are a web research assistant. Given a topic, search the web \
and return a structured summary of your findings.

Your response MUST include:
- A concise summary of the topic (2-4 paragraphs)
- Key facts as a bulleted list
- The sources or context you found

Be thorough but focused. Return ONLY the structured research results."""


def build_web_research_subagent() -> CompiledSubAgent:
    model = ChatTongyi(model_name="deepseek-v3.2")
    agent = create_agent(
        model,
        tools=[DuckDuckGoSearchRun()],
        system_prompt=_SYSTEM_PROMPT,
        name="web-research",
    )
    return CompiledSubAgent(
        name="web-research",
        description=(
            "Searches the web for information on a given topic and returns "
            "a structured research summary with key facts."
        ),
        runnable=agent,
    )
