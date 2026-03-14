import os
from pathlib import Path

from langchain_community.chat_models import ChatTongyi
from langchain_core.tools import tool

from deepagents.graph import create_agent
from deepagents.middleware.subagents import CompiledSubAgent

_REPORT_PATH = os.environ.get("REPORT_OUTPUT_PATH", "report.html")

_SYSTEM_PROMPT = """You are an HTML report generator. You receive research results about a topic.

Your job:
1. Compose a complete, self-contained HTML document from the research results.
   - Embed ALL styles inside a <style> tag — no external CSS, JS, or font links.
   - Include a clear title derived from the research topic.
   - Include the research summary and key facts, neatly formatted.
   - Include a "Generated on <timestamp>" footer.
2. Call the write_report_html tool with the complete HTML string.
3. Return the file path you received from write_report_html.

The HTML must render correctly in a browser with no internet connection."""


@tool
def write_report_html(html_content: str) -> str:
    """Write an HTML report to disk and return its absolute path.

    Args:
        html_content: The complete HTML document as a string.
    """
    output_path = Path(_REPORT_PATH).resolve()
    output_path.write_text(html_content, encoding="utf-8")
    return str(output_path)


def build_html_report_subagent() -> CompiledSubAgent:
    model = ChatTongyi(model_name="deepseek-v3.2")
    agent = create_agent(
        model,
        tools=[write_report_html],
        system_prompt=_SYSTEM_PROMPT,
        name="html-report",
    )
    return CompiledSubAgent(
        name="html-report",
        description=(
            "Takes structured research results and generates a self-contained "
            "HTML report file. Returns the absolute path to the generated report."
        ),
        runnable=agent,
    )
