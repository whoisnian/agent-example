from deepagents.graph import create_agent
from deepagents.middleware.filesystem import FilesystemMiddleware
from deepagents.middleware.subagents import CompiledSubAgent

from sandbox import DockerSandbox
from utils import get_model

_SYSTEM_PROMPT = """You are an HTML report generator. You receive research results about a topic.

Your job:
1. Compose a complete, self-contained HTML document from the research results \
already provided to you in this conversation.
   - Embed ALL styles inside a <style> tag — no external CSS, JS, or font links.
   - Include a clear title derived from the research topic.
   - Include the research summary and key facts, neatly formatted.
   - Include a "Generated on <timestamp>" footer.
2. Call the write_file tool with path "/workspace/report.html" and the complete HTML string.
3. Return the file path "/workspace/report.html".

The HTML must render correctly in a browser with no internet connection."""


def build_html_report_subagent(sandbox: DockerSandbox) -> CompiledSubAgent:
    model = get_model()
    fs = FilesystemMiddleware(backend=sandbox)
    write_file_tool = next(t for t in fs.tools if t.name == "write_file")
    agent = create_agent(
        name="html-report-agent",
        model=model,
        tools=[write_file_tool],
        system_prompt=_SYSTEM_PROMPT,
    )
    return CompiledSubAgent(
        name="html-report-agent",
        description=(
            "Takes structured research results and generates a self-contained "
            "HTML report file at /workspace/report.html inside the sandbox."
        ),
        runnable=agent,
    )
