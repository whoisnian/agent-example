import asyncio
import sys
from datetime import datetime
from pathlib import Path

from deepagents.graph import create_deep_agent

from context import CustomContext
from agents.html_report import build_html_report_subagent
from agents.web_research import build_web_research_subagent
from sandbox import DockerSandboxProvider
from utils import format_todos, get_model, truncate_str

_SYSTEM_PROMPT = """You are a research orchestrator. Given a topic from the user:

First, call write_todos to plan all pipeline steps before taking any action.

Then execute the following steps in order:
1. Use the web-research subagent to gather information about the topic.
2. Pass the full research results to the html-report subagent to generate an HTML report.
3. Use the share-html skill to upload and share the report, then report the shareable URL back to the user.
4. Report the path of the generated HTML file back to the user."""


async def main() -> None:
    model = get_model()

    topic = " ".join(sys.argv[1:]) or "What's LangChain Deep Agents?"
    print(f"Researching: {topic}")

    start_time = datetime.now()
    sandbox = DockerSandboxProvider().create()
    try:
        skills_dir = Path(__file__).parent / "skills"
        sandbox.upload_files([
            (str(f.relative_to(skills_dir.parent)), f.read_bytes())
            for f in skills_dir.rglob("*")
            if f.is_file()
        ])

        agent = create_deep_agent(
            name="main-agent",
            model=model,
            system_prompt=_SYSTEM_PROMPT,
            backend=sandbox,
            skills=["/workspace/skills/"],
            context_schema=CustomContext,
            subagents=[
                build_web_research_subagent(),
                build_html_report_subagent(sandbox),
            ],
        )

        idx = 1
        last_time = datetime.now()
        async for event in agent.astream(
            {"messages": [{"role": "user", "content": topic}]},
            stream_mode="messages",
            subgraphs=True,
            version="v2",
            context=CustomContext(start_time=start_time),
        ):
            current_time = datetime.now()
            last_duration = round((current_time - last_time).total_seconds())
            total_duration = round((current_time - start_time).total_seconds())
            print(f"\n{event.get('type')}.{idx} -------------------- {current_time.strftime('%Y-%m-%d %H:%M:%S')} -------------------- (+{last_duration}s/{total_duration}s)")
            idx += 1
            last_time = current_time

            if event.get("type") != "messages":
                continue
            token, metadata = event.get("data", (None, None))

            print(f"agent: {metadata['lc_agent_name']}")
            print(f"node:  {metadata['langgraph_node']}")
            if metadata['langgraph_node'] == 'model':
                print(f"name:  {metadata['ls_model_name']}")
            elif metadata['langgraph_node'] == 'tools':
                print(f"name:  {token.name}")
            else:
                print(f"unknown node: {metadata}")

            print(f"content: {truncate_str(token.content)}")

            if token.response_metadata and 'token_usage' in token.response_metadata:
                print(f"token_usage: {token.response_metadata['token_usage']}")

            if hasattr(token, 'tool_calls') and token.tool_calls:
                for tc in token.tool_calls:
                    if tc['name'] == 'write_todos':
                        print(f"tool_call: write_todos:\n{format_todos(tc['args']['todos'])}")
                    else:
                        print(f"tool_call: {tc['name']} args: {truncate_str(str(tc['args']))}")

        # Download report.html from sandbox to host
        responses = sandbox.download_files(["/workspace/report.html"])
        response = responses[0]
        if response.error:
            print(f"Warning: could not download report from sandbox: {response.error}")
        else:
            local_path = Path("report.html").resolve()
            local_path.write_bytes(response.content)
            print(f"Report saved to: {local_path}")
    finally:
        sandbox.stop()


if __name__ == "__main__":
    asyncio.run(main())
