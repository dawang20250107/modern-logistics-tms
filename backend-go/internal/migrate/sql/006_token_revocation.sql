-- 令牌吊销：refresh 轮换后作废旧券，以及「让某个账号的所有会话立刻失效」。
--
-- 迁移收官时自列的唯一一条「安全上应该做而没做」。原先 /auth/token/refresh
-- 只做一件事：验签通过就签发新的一对，**旧的那张 refresh 什么也没发生**。
-- 于是：
--   · 一张泄漏的 refresh 在它自然过期前（默认 7 天）一直可用，
--     而且每次用它都能换出新的 access + refresh，等于无限续期；
--   · 改密码不会踢掉任何已有会话——攻击者拿着旧 refresh 照样进；
--   · 「退出登录」在服务端是空操作，只是前端把本地存的 token 删了。
--
-- 两张机制配合，分别对付两类需求：
--
-- 1) iam_token_denylist：按 jti 精确吊销单张券。用于轮换（旧券入列）
--    与显式退出。expires_at 存的是该券自身的到期时刻，过期即可清理——
--    黑名单只需要覆盖「券还没自然死」的那段窗口，不必无限增长。
--
-- 2) accounts_user.tokens_valid_after：账号级时间水位线。改密码 / 停用 /
--    管理员重置时把它推到 now()，所有 iat 早于它的券一律失效。
--    这一条不能用黑名单做：签发过多少张券是不知道的，逐张列举本来就不可能。

CREATE TABLE IF NOT EXISTS iam_token_denylist (
    jti        text PRIMARY KEY,
    user_id    uuid,
    token_type varchar(16) NOT NULL DEFAULT 'refresh',
    reason     varchar(32)  NOT NULL DEFAULT 'rotated',
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz NOT NULL DEFAULT now()
);

-- 清理扫描按 expires_at 走
CREATE INDEX IF NOT EXISTS iam_token_denylist_expires_at_idx
    ON iam_token_denylist (expires_at);

ALTER TABLE accounts_user ADD COLUMN IF NOT EXISTS tokens_valid_after timestamptz;
