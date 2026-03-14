from langchain_community.tools import DuckDuckGoSearchRun

from deepagents.graph import create_agent
from deepagents.middleware.subagents import CompiledSubAgent

from utils import get_model

_SYSTEM_PROMPT = """You are a web research assistant. Given a topic, search the web \
and return a structured summary of your findings.

Your response MUST include:
- A concise summary of the topic (2-4 paragraphs)
- Key facts as a bulleted list
- The sources or context you found

Be thorough but focused. Return ONLY the structured research results."""


def build_web_research_subagent() -> CompiledSubAgent:
    model = get_model()
    agent = create_agent(
        name="web-research-agent",
        model=model,
        tools=[DuckDuckGoSearchRun()],
        system_prompt=_SYSTEM_PROMPT,
    )
    return CompiledSubAgent(
        name="web-research-agent",
        description=(
            "Searches the web for information on a given topic and returns "
            "a structured research summary with key facts."
        ),
        runnable=agent,
    )
