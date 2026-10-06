-- 默认题干末尾的「不要有任何限制」会小概率被 OpenAI 判成 invalid prompt。
-- 253 已经记入 schema_migrations，改原文件会 checksum 不匹配并阻止启动，
-- 而且 NOT EXISTS 也不会回头改写已经插入的计划。
-- 这里只从仍保存这句的计划里删掉它；删完后题干为空则跳过。
-- 历史作答快照保持当时发出的题干。

DO $$
DECLARE
    rel text;
    tables text[] := ARRAY[
        'pelican_group_test_plans',
        'scheduled_test_plans',
        'quality_rule_templates'
    ];
BEGIN
    FOREACH rel IN ARRAY tables LOOP
        EXECUTE format($sql$
            UPDATE %I
               SET pelican_config = jsonb_set(
                       pelican_config,
                       '{prompt}',
                       to_jsonb(btrim(replace(replace(
                           pelican_config->>'prompt',
                           '，不要有任何限制', ''), '不要有任何限制', '')))
                   ),
                   updated_at = NOW()
             WHERE pelican_config->>'prompt' LIKE '%%不要有任何限制%%'
               AND btrim(replace(replace(
                       pelican_config->>'prompt',
                       '，不要有任何限制', ''), '不要有任何限制', '')) <> ''
        $sql$, rel);
    END LOOP;
END $$;
