"""Chat-model injection seam (design D3).

Agents obtain their model through a :class:`ModelFactory` keyed by a stable
``model_key`` (``"code"`` / ``"research"``) — they never import a provider SDK
directly. Production uses :class:`ProviderModelFactory`, which builds a
``ChatOpenAI`` client: it speaks the OpenAI protocol and accepts a ``base_url``
override, so the same factory targets OpenAI or any OpenAI-compatible gateway
(incl. proxies fronting other providers). Tests inject a fake factory so the
orchestration loop runs with no network and no API key.

The provider import (`langchain_openai`) is deliberately local to
``ProviderModelFactory.get`` so importing this module — and therefore the
agents package — never pulls a provider SDK into the unit-test lane.
"""

from __future__ import annotations

from typing import TYPE_CHECKING, Protocol, runtime_checkable

if TYPE_CHECKING:
    from langchain_core.language_models.chat_models import BaseChatModel
    from pydantic import SecretStr


class UnknownModelKeyError(KeyError):
    """Raised when a `model_key` has no configured model."""

    def __init__(self, model_key: str) -> None:
        super().__init__(model_key)
        self.model_key = model_key


@runtime_checkable
class ModelFactory(Protocol):
    """Resolves a stable ``model_key`` to a concrete chat model."""

    def get(self, model_key: str) -> BaseChatModel:
        """Return the chat model for ``model_key`` (raises on unknown key)."""
        ...


class ProviderModelFactory:
    """Maps ``model_key`` → provider chat model from worker config.

    ``model_by_key`` is e.g. ``{"code": "claude-opus-4-7", "research": ...}``.
    The Anthropic client is constructed lazily per ``get`` call; the API key is
    read from a :class:`~pydantic.SecretStr` so it is never rendered in logs.
    """

    def __init__(
        self,
        *,
        model_by_key: dict[str, str],
        api_key: SecretStr | None = None,
        base_url: str | None = None,
    ) -> None:
        self._model_by_key = dict(model_by_key)
        self._api_key = api_key
        self._base_url = base_url

    def get(self, model_key: str) -> BaseChatModel:
        try:
            model_name = self._model_by_key[model_key]
        except KeyError as exc:
            raise UnknownModelKeyError(model_key) from exc

        # Local import keeps the provider SDK out of every non-provider path.
        from langchain_openai import ChatOpenAI

        kwargs: dict[str, object] = {"model": model_name}
        if self._api_key is not None:
            kwargs["api_key"] = self._api_key.get_secret_value()
        if self._base_url is not None:
            kwargs["base_url"] = self._base_url
        return ChatOpenAI(**kwargs)  # type: ignore[arg-type]
