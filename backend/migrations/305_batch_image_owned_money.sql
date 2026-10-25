-- Version-2 jobs only. Never infer historical holds from pooled frozen money.
CREATE TABLE batch_image_money_holds (
    batch_id VARCHAR(64) NOT NULL,
    api_key_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    hold_amount NUMERIC(20,8) NOT NULL CHECK (hold_amount>=0),
    actual_amount NUMERIC(20,8),
    state TEXT NOT NULL CHECK (state IN ('held','captured','released')),
    PRIMARY KEY (batch_id,api_key_id)
);
