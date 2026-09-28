-- 鹈鹕测智改为按分组测试：计划绑定分组，每次由网关调度器按真实请求的规则挑选账号作答，
-- 成功的 HTML 发布到该分组的用户展示页；有计划的分组才出现在展示页。
-- 账号级鹈鹕定时测试保留（管理员排查用），但不再发布到展示页。
-- 旧版本二进制不读写这两张表，可继续运行；回滚后需在系统设置里重新选择展示分组。
CREATE TABLE IF NOT EXISTS pelican_group_test_plans (
    id              BIGSERIAL    PRIMARY KEY,
    group_id        BIGINT       NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    model_id        VARCHAR(100) NOT NULL DEFAULT '',
    cron_expression VARCHAR(100) NOT NULL DEFAULT '*/30 * * * *',
    enabled         BOOLEAN      NOT NULL DEFAULT false,
    pelican_config  JSONB        NOT NULL,
    last_run_at     TIMESTAMPTZ,
    next_run_at     TIMESTAMPTZ,
    running_until   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pelican_group_test_plans_group
    ON pelican_group_test_plans (group_id);
CREATE INDEX IF NOT EXISTS idx_pelican_group_test_plans_due
    ON pelican_group_test_plans (next_run_at) WHERE enabled = true;

-- 与 scheduled_test_results 共用 ID 序列：两者的 ID 都用作 pelican_showcase_items.source_result_id，
-- 共用序列保证与展示表里旧的账号级快照不会撞号。
CREATE TABLE IF NOT EXISTS pelican_group_test_results (
    id             BIGINT      PRIMARY KEY DEFAULT nextval('scheduled_test_results_id_seq'),
    plan_id        BIGINT      NOT NULL REFERENCES pelican_group_test_plans(id) ON DELETE CASCADE,
    account_id     BIGINT,
    account_name   TEXT        NOT NULL DEFAULT '',
    attempts       JSONB       NOT NULL DEFAULT '[]'::jsonb,
    status         VARCHAR(20) NOT NULL,
    response_text  TEXT        NOT NULL DEFAULT '',
    error_message  TEXT        NOT NULL DEFAULT '',
    latency_ms     BIGINT      NOT NULL DEFAULT 0,
    pelican_config JSONB,
    started_at     TIMESTAMPTZ NOT NULL,
    finished_at    TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pelican_group_test_results_plan
    ON pelican_group_test_results (plan_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_pelican_group_test_results_created
    ON pelican_group_test_results (created_at);

COMMENT ON TABLE pelican_group_test_plans IS
    '鹈鹕测智分组测试计划：按 Cron 定时出题，账号由网关调度器按真实请求规则挑选';
COMMENT ON COLUMN pelican_group_test_results.account_id IS
    '最终作答的账号（不设外键，账号删除后保留记录）；NULL 表示没有选到可用账号';
COMMENT ON COLUMN pelican_group_test_results.attempts IS
    '出内容前就报错而换掉的账号：[{account_id, account_name, error}]，按尝试顺序';
COMMENT ON COLUMN pelican_showcase_items.source_result_id IS
    '来源结果 ID：pelican_group_test_results.id（分组测试）或 scheduled_test_results.id（旧版账号级测试），两表共用序列；仅用于去重与追溯，不设外键';

-- 已选的展示分组转为「已暂停」的分组测试，分组继续出现在展示页（旧作品照常显示），
-- 管理员确认模型后开启即可。模型和思考强度取该分组最新一张展示作品，取不到时留空 / medium。
-- 配置损坏时跳过，不影响升级；管理员可在新页面重新添加。
DO $$
DECLARE
    selected BIGINT[];
BEGIN
    BEGIN
        SELECT ARRAY(SELECT jsonb_array_elements_text(value::jsonb -> 'group_ids')::BIGINT)
          INTO selected
          FROM settings
         WHERE key = 'pelican_showcase_config';
    EXCEPTION WHEN others THEN
        selected := NULL;
    END;
    IF selected IS NULL OR cardinality(selected) = 0 THEN
        RETURN;
    END IF;
    INSERT INTO pelican_group_test_plans (group_id, model_id, enabled, pelican_config)
    SELECT g.id,
           COALESCE(latest.model_id, ''),
           false,
           jsonb_build_object(
               'question_kind', 'pelican',
               'prompt', '创建一个 HTML，内容是 SVG 绘制一个鹈鹕骑自行车的 2D 动画，你不需要任何测试，不要有任何限制',
               'reasoning_effort', CASE WHEN latest.reasoning_effort IN ('minimal', 'low', 'medium', 'high', 'xhigh')
                                        THEN latest.reasoning_effort ELSE 'medium' END,
               'parallel_count', 1)
      FROM groups g
      LEFT JOIN LATERAL (
          SELECT i.model_id, i.reasoning_effort
            FROM pelican_showcase_items i
           WHERE i.group_id = g.id
           ORDER BY i.generated_at DESC, i.id DESC
           LIMIT 1
      ) latest ON true
     WHERE g.id = ANY(selected)
       AND g.deleted_at IS NULL
       AND NOT EXISTS (SELECT 1 FROM pelican_group_test_plans p WHERE p.group_id = g.id);
END $$;
