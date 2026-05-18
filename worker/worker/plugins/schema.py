"""Pydantic schema for ``plugin.yaml`` files.

Matches ``docs/ARCHITECTURE.md §8.2``. Unknown fields are rejected so typos
are caught at startup (design D10).
"""

from __future__ import annotations

from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field


class PluginPermissions(BaseModel):
    """List-of-strings permissions block.

    Modeled as a list rather than a structured object because the architecture
    spec uses free-form strings (``oss.write:tmp/`` etc.).
    """

    model_config = ConfigDict(extra="forbid", frozen=True)


class PluginAppliesTo(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)
    task_types: list[str] = Field(default_factory=list)


class PluginResources(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)
    timeout_s: int | None = None
    memory_mb: int | None = None


class PluginManifest(BaseModel):
    """Top-level schema for ``plugin.yaml``."""

    model_config = ConfigDict(extra="forbid", frozen=True)

    kind: Literal["tool", "subagent", "skill"]
    name: str = Field(min_length=1)
    version: str = Field(min_length=1)
    entrypoint: str = Field(
        min_length=1,
        description="Python import path of the form ``module:callable``",
    )
    schema_: dict[str, Any] | None = Field(default=None, alias="schema")
    permissions: list[str] = Field(default_factory=list)
    applies_to: PluginAppliesTo = Field(default_factory=PluginAppliesTo)
    resources: PluginResources = Field(default_factory=PluginResources)

    def parsed_entrypoint(self) -> tuple[str, str]:
        if ":" not in self.entrypoint:
            raise ValueError(f"entrypoint must be 'module:callable', got {self.entrypoint!r}")
        module, _, attr = self.entrypoint.partition(":")
        return module, attr
