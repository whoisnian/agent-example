-- 0009_task_versions_summary (up)
--
-- refactor-task-conversation-continuity: 版本结果摘要列。
-- Worker 在 run 成功收尾发出 kind=summary 事件，API 事件消费端经
-- Domain Service（ApplyVersionSummary）幂等回写本列；后续 iterate /
-- rollback-branch 沿 parent_id 链组装对话历史时读取。
-- 可空：失败/取消的 run 不产生摘要，本迁移之前的存量版本恒为 NULL。
ALTER TABLE task_versions
    ADD COLUMN summary TEXT;
