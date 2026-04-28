import argparse
import asyncio
import json
import uuid
from datetime import datetime
from pathlib import Path

from deepagents.graph import create_deep_agent
from langchain_core.messages import AIMessageChunk, ToolMessage
from langgraph.checkpoint.sqlite.aio import AsyncSqliteSaver

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


def _print_accumulated_tools(tool_args: dict) -> None:
    """Print accumulated tool call info at the end of a model turn."""
    for tc_id, info in tool_args.items():
        name = info["name"]
        args_str = info["args"]
        if name == "write_todos":
            try:
                parsed = json.loads(args_str)
                print(f"    call: write_todos\n{format_todos(parsed.get('todos', []))}")
            except (json.JSONDecodeError, TypeError):
                print(f"    call: write_todos args: {truncate_str(args_str)}")
        else:
            print(f"    call: {name} args: {truncate_str(args_str)}")


async def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--thread-id", default=None)
    parser.add_argument("--debug", action="store_true", help="Write raw events to a debug log in /tmp")
    parser.add_argument("topic", nargs="*")
    args = parser.parse_args()

    thread_id = args.thread_id or str(uuid.uuid4())
    topic = " ".join(args.topic) or "What's LangChain Deep Agents?"

    print(f"Thread ID: {thread_id}")
    print(f"Researching: {topic}")

    model = get_model()
    start_time = datetime.now()
    sandbox = DockerSandboxProvider().create()
    try:
        skills_dir = Path(__file__).parent / "skills"
        sandbox.upload_files([
            (str(f.relative_to(skills_dir.parent)), f.read_bytes())
            for f in skills_dir.rglob("*")
            if f.is_file()
        ])

        async with AsyncSqliteSaver.from_conn_string("checkpoints.db") as checkpointer:
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
                checkpointer=checkpointer,
            )

            idx = 1
            last_time = datetime.now()
            if args.debug:
                log_path = Path(f"/tmp/agent-example.events.{uuid.uuid4().hex[:8]}.log")
                log_file = log_path.open("w")
                print(f"Debug log: {log_path}")
            else:
                log_file = None

            # Track current turn to avoid repeating headers
            current_run_id = None
            current_node = None
            current_agent = None
            accumulated_tool_args = {}  # tool_call_id -> {"name": str, "args": str}
            accumulated_content = ""

            async for event in agent.astream(
                {"messages": [{"role": "user", "content": topic}]},
                stream_mode="messages",
                subgraphs=True,
                version="v2",
                config={"configurable": {"thread_id": thread_id}},
                context=CustomContext(thread_id=thread_id, start_time=start_time),
            ):
                if log_file:
                    log_file.write(repr(event) + "\n")
                    log_file.flush()

                if event.get("type") != "messages":
                    continue
                token, metadata = event.get("data", (None, None))
                if token is None:
                    continue

                agent_name = metadata.get('lc_agent_name', '?')
                node = metadata.get('langgraph_node', '?')
                run_id = token.id if hasattr(token, 'id') else None

                # Print header when turn changes (new run or node switch)
                is_new_turn = (run_id != current_run_id or node != current_node
                               or agent_name != current_agent)
                if is_new_turn:
                    # Flush previous turn's accumulated content
                    if accumulated_content:
                        print()
                        accumulated_content = ""
                    if accumulated_tool_args:
                        _print_accumulated_tools(accumulated_tool_args)
                        accumulated_tool_args = {}

                    current_run_id = run_id
                    current_node = node
                    current_agent = agent_name

                    current_time = datetime.now()
                    last_duration = round((current_time - last_time).total_seconds())
                    total_duration = round((current_time - start_time).total_seconds())
                    print(f"\n--- {idx}. [{agent_name}] {node} --- "
                          f"{current_time.strftime('%H:%M:%S')} "
                          f"(+{last_duration}s/{total_duration}s) ---")
                    idx += 1
                    last_time = current_time

                    if node == 'model':
                        model_name = metadata.get('ls_model_name', '?')
                        print(f"    model: {model_name}")

                # Handle ToolMessage (tool responses)
                if isinstance(token, ToolMessage):
                    tool_name = getattr(token, 'name', '?')
                    content = token.content or ''
                    print(f"    tool: {tool_name}")
                    print(f"    result: {truncate_str(content)}")
                    continue

                # Handle AIMessageChunk (model streaming)
                if isinstance(token, AIMessageChunk):
                    # Stream text content
                    if token.content:
                        print(token.content, end="", flush=True)
                        accumulated_content += token.content

                    # Accumulate tool call chunks
                    for tc_chunk in (token.tool_call_chunks or []):
                        tc_id = tc_chunk.get('id') or 'pending'
                        if tc_id not in accumulated_tool_args and tc_chunk.get('name'):
                            accumulated_tool_args[tc_id] = {
                                "name": tc_chunk['name'], "args": ""}
                        if tc_id in accumulated_tool_args and tc_chunk.get('args'):
                            accumulated_tool_args[tc_id]["args"] += tc_chunk['args']
                        elif tc_id not in accumulated_tool_args and tc_chunk.get('args'):
                            # Continuation chunk for the most recent tool call
                            if accumulated_tool_args:
                                last_key = list(accumulated_tool_args)[-1]
                                accumulated_tool_args[last_key]["args"] += tc_chunk['args']

                    # Check for finish
                    finish = token.response_metadata.get(
                        'finish_reason') if token.response_metadata else None
                    if finish or getattr(token, 'chunk_position', None) == 'last':
                        if accumulated_content:
                            print()
                            accumulated_content = ""
                        if accumulated_tool_args:
                            _print_accumulated_tools(accumulated_tool_args)
                            accumulated_tool_args = {}
                        if token.response_metadata.get('token_usage'):
                            print(f"    usage: {token.response_metadata['token_usage']}")

            if log_file:
                log_file.close()

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
