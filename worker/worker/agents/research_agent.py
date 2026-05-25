"""Research agent (``task_type=research``).

Investigates a question and writes a Markdown report artifact. It is a
:class:`~worker.agents.base.LoopAgent` parametrized with the research spec
(oss_fs + web_search tools, ``research`` model key).
"""

from __future__ import annotations

from pathlib import Path
from typing import TYPE_CHECKING, Any

from worker.agents.base import AgentSpec, LoopAgent
from worker.agents.tools import oss_write_file

if TYPE_CHECKING:
    from worker.agents.model import ModelFactory
    from worker.core.config import Settings
    from worker.core.persistence import Persistence

_PROMPT_PATH = Path(__file__).resolve().parent / "prompts" / "research_system.md"

RESEARCH_AGENT_SPEC = AgentSpec(
    task_type="research",
    model_key="research",
    system_prompt_path=_PROMPT_PATH,
    tool_names=("oss_fs", "web_search"),
)


def build_research_agent(
    model_factory: ModelFactory,
    persistence: Persistence,
    settings: Settings,
    metrics: Any | None = None,
) -> LoopAgent:
    return LoopAgent(
        spec=RESEARCH_AGENT_SPEC,
        model_factory=model_factory,
        persistence=persistence,
        write_file=oss_write_file,
        max_step_retries=settings.max_step_retries,
        metrics=metrics,
    )
