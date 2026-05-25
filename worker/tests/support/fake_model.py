"""Scripted fake chat model + factory (design D3 test seam).

Lets the orchestration loop run end-to-end with no network and no API key.
A :class:`ScriptedChatModel` replays a fixed list of ``AIMessage`` responses
in order; because it runs through LangChain's standard ``generate`` path, it
fires ``on_llm_start`` / ``on_llm_end`` callbacks, so :class:`CostMeter` is
exercised exactly as it would be against a real provider.

The plan/execute/critic loop (later slice) treats each model turn as one
scripted response; tests assemble the script to drive the desired transcript.
"""

from __future__ import annotations

from langchain_core.language_models.chat_models import BaseChatModel
from langchain_core.language_models.fake_chat_models import FakeMessagesListChatModel
from langchain_core.messages import AIMessage


def scripted_model(responses: list[str | AIMessage]) -> FakeMessagesListChatModel:
    """Build a chat model that replays ``responses`` (str or AIMessage) in order."""
    messages = [AIMessage(content=r) if isinstance(r, str) else r for r in responses]
    return FakeMessagesListChatModel(responses=messages)


class FakeModelFactory:
    """`ModelFactory` test double returning scripted models per ``model_key``.

    Construct with either a single shared model (used for every key) or a
    per-key mapping. Implements the ``ModelFactory`` protocol structurally.
    """

    def __init__(
        self,
        *,
        model: BaseChatModel | None = None,
        model_by_key: dict[str, BaseChatModel] | None = None,
    ) -> None:
        if (model is None) == (model_by_key is None):
            raise ValueError("provide exactly one of `model` or `model_by_key`")
        self._model = model
        self._model_by_key = dict(model_by_key) if model_by_key else None

    def get(self, model_key: str) -> BaseChatModel:
        if self._model is not None:
            return self._model
        assert self._model_by_key is not None
        return self._model_by_key[model_key]
