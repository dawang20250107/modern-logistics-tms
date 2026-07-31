package auth

// 令牌吊销：轮换作废、显式退出、账号级"踢掉全部会话"。
//
// 背景与两张机制的分工见 006_token_revocation.sql 的注释。这里只讲一处
// 容易写错的地方：**校验必须发生在签发新券之前**。
// 若先签发再检查旧券，攻击者用一张已入黑名单的券也能拿到新券，
// 黑名单就只是记了个账。

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 吊销原因，写进 iam_token_denylist.reason，便于事后追查
const (
	ReasonRotated  = "rotated"  // 轮换：换出新券时作废旧券
	ReasonLogout   = "logout"   // 用户主动退出
	ReasonPassword = "password" // 改密/重置密码
)

// Revoke 把一张券按 jti 加入黑名单。
// exp 传该券自身的到期时刻——黑名单只需覆盖到券自然死为止。
func Revoke(ctx context.Context, db *pgxpool.Pool, jti, userID, tokenType, reason string, exp time.Time) error {
	var uid any
	if userID != "" {
		uid = userID
	}
	_, err := db.Exec(ctx, `
		INSERT INTO iam_token_denylist (jti, user_id, token_type, reason, expires_at, revoked_at)
		VALUES ($1, $2::uuid, $3, $4, $5, now())
		ON CONFLICT (jti) DO NOTHING`,
		jti, uid, tokenType, reason, exp)
	return err
}

// IsRevoked 该 jti 是否已被吊销。
//
// 查库失败时返回 true（视为已吊销）。这是刻意的：鉴权路径上"不确定"必须
// 倒向拒绝，否则数据库一抖动，吊销就整体失效——那正是最需要它生效的时候。
func IsRevoked(ctx context.Context, db *pgxpool.Pool, jti string) bool {
	var n int
	if err := db.QueryRow(ctx,
		`SELECT count(*) FROM iam_token_denylist WHERE jti = $1`, jti).Scan(&n); err != nil {
		return true
	}
	return n > 0
}

// RevokeAllForUser 把某账号的全部现存令牌作废（改密 / 停用 / 管理员重置 / 退出全部会话）。
// 走时间水位线而不是黑名单：签发过多少张券本来就无从枚举。
//
// 水位线**截断到秒**，配合 IssuedBeforeCutoff 的严格小于比较，得到的语义是：
// 「本秒之前签发的一律作废，本秒之内签发的放行」。
//
// 这不是偷懒，是 JWT 的 iat 只有秒精度导致的必然取舍——两种做法二选一：
//
//	· 水位线取 now() 全精度：同一秒签发的券全被踢。于是「改完密码顺手发一对新券」
//	  这个动作做不成，用户改个密码就被登出，而这一步本可以无缝。
//	· 水位线截断到秒：换来最多 1 秒的窗口，该窗口内签发的旧券能多活到自然过期。
//
// 取后者。这 1 秒对 refresh 无影响（它按 jti 精确入黑名单），
// 只影响恰好在同一秒签发的 access，而 access 本身就是短命的。
func RevokeAllForUser(ctx context.Context, db *pgxpool.Pool, userID string) error {
	_, err := db.Exec(ctx,
		`UPDATE accounts_user SET tokens_valid_after = date_trunc('second', now()) WHERE id = $1::uuid`,
		userID)
	return err
}

// IssuedBeforeCutoff 该券的签发时间是否早于账号的水位线（早于则应拒绝）。
//
// 同样对错误取保守侧。水位线为 NULL（从未整体吊销过）时一律放行。
func IssuedBeforeCutoff(ctx context.Context, db *pgxpool.Pool, userID string, issuedAt time.Time) bool {
	var cutoff *time.Time
	if err := db.QueryRow(ctx,
		`SELECT tokens_valid_after FROM accounts_user WHERE id = $1::uuid`, userID).Scan(&cutoff); err != nil {
		return true
	}
	if cutoff == nil {
		return false
	}
	// 水位线在写入时已截断到秒（见 RevokeAllForUser），这里直接严格小于即可：
	// 同一秒签发的券 iat == cutoff，不小于，放行。
	return issuedAt.Before(*cutoff)
}

// PurgeExpiredDenylist 清理已自然过期的黑名单条目。
// 黑名单只需覆盖「券还没死」的窗口，过期条目留着只会让表无限长。
func PurgeExpiredDenylist(ctx context.Context, db *pgxpool.Pool) (int64, error) {
	tag, err := db.Exec(ctx, `DELETE FROM iam_token_denylist WHERE expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// StartDenylistPurger 每 interval 清一次过期黑名单条目。
func StartDenylistPurger(ctx context.Context, db *pgxpool.Pool, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			_, _ = PurgeExpiredDenylist(ctx, db)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}
