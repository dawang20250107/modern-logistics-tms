-- 把三处「进程内 map」搬到库里：登录失败锁定、限流计数、找回密码验证码。
--
-- 这三处原本都是 `var mu sync.Mutex; var m = map[string]...{}`，注释里也写着
-- 「多实例部署时需换成共享存储」。多副本时各自失效的方式不同，但都很难看：
--
--   登录锁定  5 次失败锁定，两个副本就是 10 次 —— 暴力破解的成本直接翻倍地降
--   限流      注册 10/min、找回密码 8/min、AI 30/min，副本数一乘就形同虚设
--   验证码    A 副本发的码，请求落到 B 副本时查无此码，用户永远重置不了密码
--
-- 前两条是安全闸，第三条是功能断裂。而"要不要多副本"本不该由这几个 map 决定。
--
-- 都带 expires_at 并建索引：这些是短命状态，过期即清，表不该无限长。
-- 用库而不是 Redis：这套系统迁移后已经没有 Redis 了，为三张小表再引一个
-- 中间件不划算；这些写入的量级（登录、注册、找回密码）也远够不上 PG 的瓶颈。

-- 登录失败计数与锁定。key = 规范化后的用户名。
CREATE TABLE IF NOT EXISTS iam_login_throttle (
    username    text PRIMARY KEY,
    failures    integer     NOT NULL DEFAULT 0,
    window_end  timestamptz NOT NULL,   -- 计数窗口结束时刻，过了就重新计
    locked_until timestamptz,           -- 非空且未过即锁定中
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS iam_login_throttle_window_end_idx ON iam_login_throttle (window_end);

-- 通用滑动窗口限流。scope = 闸名（register/password_reset/ai），key = IP 或用户 ID。
-- 一行一次命中，按 (scope,key) 数窗口内的行数；简单、并发安全、清理容易。
CREATE TABLE IF NOT EXISTS iam_rate_hit (
    id         bigserial PRIMARY KEY,
    scope      text        NOT NULL,
    key        text        NOT NULL,
    hit_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS iam_rate_hit_scope_key_at_idx ON iam_rate_hit (scope, key, hit_at);

-- 找回密码验证码。一次性：核销即删。
CREATE TABLE IF NOT EXISTS iam_reset_code (
    identifier text PRIMARY KEY,          -- 用户提交的标识（邮箱/手机号/用户名）
    code       text        NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS iam_reset_code_expires_at_idx ON iam_reset_code (expires_at);
