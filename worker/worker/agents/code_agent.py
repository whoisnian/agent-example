"""Code-generation agent (``task_type=code-gen``).

Produces source files only — no code-execution tool, keeping the MVP inside the
no-sandbox boundary (design Open Q#2 / ARCHITECTURE §8.4). It is a
:class:`~worker.agents.base.LoopAgent` parametrized with the code-gen spec.
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

_PROMPT_PATH = Path(__file__).resolve().parent / "prompts" / "code_system.md"

CODE_AGENT_SPEC = AgentSpec(
    task_type="code-gen",
    model_key="code",
    system_prompt_path=_PROMPT_PATH,
    tool_names=("oss_fs",),
)


def build_code_agent(
    model_factory: ModelFactory,
    persistence: Persistence,
    settings: Settings,
    metrics: Any | None = None,
) -> LoopAgent:
    return LoopAgent(
        spec=CODE_AGENT_SPEC,
        model_factory=model_factory,
        persistence=persistence,
        write_file=oss_write_file,
        max_step_retries=settings.max_step_retries,
        metrics=metrics,
    )
