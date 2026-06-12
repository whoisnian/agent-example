"""Wire-format parsing tests for ``TaskExecuteMessage`` (add-semantic-task-title)."""

from __future__ import annotations

import json
from typing import Any
from uuid import uuid4

from worker.core.messages import TaskExecuteMessage


def _execute_payload(**overrides: Any) -> dict[str, Any]:
    payload: dict[str, Any] = {
        "msg_id": str(uuid4()),
        "idempotency_key": str(uuid4()),
        "task_id": str(uuid4()),
        "version_id": str(uuid4()),
        "run_id": str(uuid4()),
        "attempt_no": 1,
        "task_type": "code-gen",
        "prompt": "build a music app",
        "params": {},
        "parent_version_id": None,
        "parent_artifact_root": None,
        "deadline_ts": None,
    }
    payload.update(overrides)
    return payload


def test_absent_gen_title_defaults_to_false() -> None:
    msg = TaskExecuteMessage.model_validate_json(json.dumps(_execute_payload()))
    assert msg.gen_title is False


def test_gen_title_true_is_parsed() -> None:
    msg = TaskExecuteMessage.model_validate_json(json.dumps(_execute_payload(gen_title=True)))
    assert msg.gen_title is True


def test_unknown_extra_field_is_not_poison() -> None:
    # 新增未知字段不得触发 poison → DLX（spec: Task Execute Consumer）。
    raw = json.dumps(_execute_payload(some_future_field={"nested": 1}))
    msg = TaskExecuteMessage.model_validate_json(raw)
    assert msg.task_type == "code-gen"
    assert msg.gen_title is False


# --- history (task-conversation-history / worker-messaging) -----------------


def test_absent_history_defaults_to_empty_list() -> None:
    msg = TaskExecuteMessage.model_validate_json(json.dumps(_execute_payload()))
    assert msg.history == []
    assert msg.history_invalid is False


def test_valid_history_parses_with_nullable_summary() -> None:
    raw = json.dumps(
        _execute_payload(
            history=[
                {
                    "version_no": 1,
                    "prompt": "build app",
                    "summary": "did v1",
                    "status": "succeeded",
                },
                {"version_no": 2, "prompt": "fix bug", "summary": None, "status": "failed"},
            ]
        )
    )
    msg = TaskExecuteMessage.model_validate_json(raw)
    assert [t.version_no for t in msg.history] == [1, 2]
    assert msg.history[0].summary == "did v1"
    assert msg.history[1].summary is None
    assert msg.history[1].status == "failed"
    assert msg.history_invalid is False


def test_invalid_history_degrades_to_empty_not_poison() -> None:
    # 结构非法的 history 降级为空 + 标记，不得 poison → DLX（spec: Task Execute
    # Consumer → "Structurally invalid history degrades instead of poisoning"）。
    raw = json.dumps(_execute_payload(history=[{"version_no": 1}]))  # missing prompt
    msg = TaskExecuteMessage.model_validate_json(raw)
    assert msg.history == []
    assert msg.history_invalid is True


def test_non_list_history_degrades_to_empty_not_poison() -> None:
    raw = json.dumps(_execute_payload(history="garbage"))
    msg = TaskExecuteMessage.model_validate_json(raw)
    assert msg.history == []
    assert msg.history_invalid is True
