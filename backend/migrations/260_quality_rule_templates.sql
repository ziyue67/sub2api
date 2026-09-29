-- 质量运维「分组规则」：按账户筛选（分组 / 类型 / 状态 / 搜索）保存的规则模板。
-- 后台定时把模板补建到新匹配的账户上；关联表记下每个已经建过的账户，
-- 管理员手动删掉的单条规则不会被重新建（关联行还在，plan_id 置空）。
CREATE TABLE IF NOT EXISTS quality_rule_templates (
    id BIGSERIAL PRIMARY KEY,
    account_filter JSONB NOT NULL DEFAULT '{}'::jsonb,
    model_id VARCHAR(100) NOT NULL DEFAULT '',
    cron_expression VARCHAR(100) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    max_results INT NOT NULL DEFAULT 100,
    pelican_config JSONB NOT NULL,
    last_synced_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS quality_rule_template_accounts (
    template_id BIGINT NOT NULL REFERENCES quality_rule_templates(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    plan_id BIGINT REFERENCES scheduled_test_plans(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (template_id, account_id)
);

CREATE INDEX IF NOT EXISTS idx_quality_rule_template_accounts_plan ON quality_rule_template_accounts(plan_id);
