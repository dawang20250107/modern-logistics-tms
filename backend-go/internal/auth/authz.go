package auth

// 请求级鉴权闸：一次调用同时拿到「你是谁」「你有没有这个权限点」「你能看哪些组织的数据」。
//
// 为什么要有这个文件——
// 迁移完成时，155 条路由里手写 handler 的权限判断散在 5 个包，各自复制了一份
// 一模一样的 hasPerm，调用形状也各写各的：
//
//	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
//	if err != nil { 401 }
//	_, _, perms, err := h.Svc.RolesAndPerms(ctx, me)
//	if err != nil || !hasPerm(perms, "waybill.view") { 403 }
//	scopeIDs, err := h.Svc.ScopeOrgIDs(ctx, me)
//	if err != nil { 500 }
//	if scopeIDs != nil { ...拼 where... }
//
// 六行样板、四个出口、五份 hasPerm 副本。这种形状的代码只有一个结局：
// 某个域会漏掉它。**而漏掉的恰好是财务**——因为财务是唯一从零重写、
// 没有照着 Django 的 HasPermission 装饰器逐条抄的域。实测后果：
// 一个刚 /auth/register 出来的账号（无角色、无权限点、无组织）能读到
// /finance/statement-overview 的全量应收敞口、/finance/aging 的逐客户账龄、
// /finance/statements 的整本台账；POST /finance/statements/{id}/settle
// 返回的是 404 而不是 403——404 意味着它压根没被鉴权拦住，只是那个 UUID
// 不存在而已，换成真实单号就能把别人的钱标记成已收。
//
// 所以把这段样板收进一处：调用点写一行，忘不掉，也没法各写一套。
// hasPerm 只此一份（`*` 通配符语义与 Django 的 HasPermission 一致）。

import (
	"fmt"
	"net/http"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// Actor 一次鉴权的结果。ScopeIDs 语义与 ScopeOrgIDs 相同：
// nil = 全量可见；空切片 = 任何归属组织的数据都不可见（例如账号没挂组织）。
type Actor struct {
	User     *UserRow
	Perms    []string
	ScopeIDs []string
}

// HasPerm 权限点判断。`*` 是超管的通配符。
func HasPerm(perms []string, want string) bool {
	for _, p := range perms {
		if p == "*" || p == want {
			return true
		}
	}
	return false
}

// Has 当前主体是否具备某权限点（同一 handler 内的二次判断，如读通过后再判写）。
func (a *Actor) Has(want string) bool { return HasPerm(a.Perms, want) }

// ArgList 是 filters.Args 的最小接口，避免 auth 反向依赖 filters 包。
type ArgList interface{ Add(v any) string }

// ScopeSQL 把数据范围拼成一段 WHERE 片段。
//
// col 传该表的组织列表达式（如 "w.organization_id::text"）。
// 返回 "true" 表示不限制；返回 "false" 表示一条也看不见——
// 后者不能省略成 "true"：账号没挂组织时"看不见任何东西"才是正确答案，
// 放行等于把范围控制反向做成了全量可见。
func (a *Actor) ScopeSQL(col string, args ArgList) string {
	if a.ScopeIDs == nil {
		return "true"
	}
	if len(a.ScopeIDs) == 0 {
		return "false"
	}
	return fmt.Sprintf("%s = ANY(%s)", col, args.Add(a.ScopeIDs))
}

// Guard 鉴权闸。want 为空串表示只要求登录、不校验权限点。
//
// 返回 nil 时响应已经写好（401/403/500），调用方直接 return 即可。
func (s *Service) Guard(w http.ResponseWriter, r *http.Request, want, denyMsg string) *Actor {
	ctx := r.Context()
	me, err := s.UserByID(ctx, UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return nil
	}
	_, _, perms, err := s.RolesAndPerms(ctx, me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取权限失败")
		return nil
	}
	if want != "" && !HasPerm(perms, want) {
		if denyMsg == "" {
			denyMsg = "无操作权限"
		}
		httpx.Err(w, http.StatusForbidden, "PERMISSION_DENIED", denyMsg)
		return nil
	}
	scopeIDs, err := s.ScopeOrgIDs(ctx, me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return nil
	}
	return &Actor{User: me, Perms: perms, ScopeIDs: scopeIDs}
}
