package main

// 权限矩阵这条链：管理员在界面上勾一个格子，那个人到底有没有拿到权限。
//
// 为什么单独要这条：authz_test.go 那一整套是**用 SQL 直接授权**的
// （mkUser 往 iam_role_permissions 里插一行），验的是"权限点挂对了闸没有"。
// 但管理员在界面上走的完全是另一条路：
//
//   GET  /org/rbac/matrix                 ← 界面上那张勾选表从哪来
//   POST /org/roles/{id}/set-permissions  ← 勾完点保存
//   POST /org/employees/{id}/roles        ← 把角色发给人
//
// 这三步里任何一步写错地方，SQL 授权的用例照样全绿，而现实中**没有一个管理员
// 能真正授出权限**——整套 RBAC 变成摆设。这一轮已经踩过一次同款：
// 催办接口写 status='sent'，而司机端队列读的是 status='pending'，
// 接口 201、界面绿、司机永远看不到。写的地方和读的地方对不上，
// 只有把两头接起来跑一次才看得见。
//
// 所以这条用例全程走 HTTP，一句 INSERT 都不用：
// 授权前 403 → 走完界面那三步 → 同一个人同一个端点 200。

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestPermissionMatrixRoundTripActuallyGrants(t *testing.T) {
	e := newTestEnv(t)
	admin := e.mkUser(true) // 超管：界面上能操作权限矩阵的那种人

	// 目标权限点选 finance.view——财务域正是当初那个洞的所在地
	const wantPerm = "finance.view"
	const guarded = "/api/v1/finance/statement-overview"

	// ── 1. 界面上那张勾选表：矩阵接口给出的 permission id ──
	rec := e.call(admin, "GET", "/api/v1/org/rbac/matrix", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("读权限矩阵返回 %d：%s", rec.Code, rec.Body.String())
	}
	var matrix struct {
		Data struct {
			Modules []struct {
				Permissions []struct{ ID, Code string } `json:"permissions"`
			} `json:"modules"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &matrix); err != nil {
		t.Fatalf("矩阵响应解不开：%v\n%s", err, rec.Body.String())
	}
	permID := ""
	total := 0
	for _, m := range matrix.Data.Modules {
		for _, p := range m.Permissions {
			total++
			if p.Code == wantPerm {
				permID = p.ID
			}
		}
	}
	// 防空转：矩阵是空的话，下面"授权后能访问"会因为别的原因通过或失败，
	// 而这条用例会看起来跑过了。
	if total == 0 {
		t.Fatal("权限矩阵一个权限点都没返回——界面上那张勾选表是空的，管理员无从授权")
	}
	if permID == "" {
		t.Fatalf("权限矩阵里没有 %s（共 %d 个权限点）：界面上根本勾不到它，"+
			"任何角色都无法被授予这个权限", wantPerm, total)
	}

	// ── 2. 建一个角色，并把该权限点勾上（走界面用的那两个接口）──
	rid, _ := uuid.NewV7()
	roleCode := "rbac_rt_" + rid.String()
	if _, err := e.pool.Exec(t.Context(), `
		INSERT INTO iam_role (id, created_at, updated_at, code, name, data_scope, is_active)
		VALUES ($1::uuid, now(), now(), $2, $2, 'all', true)`, rid.String(), roleCode); err != nil {
		t.Fatalf("建角色失败：%v", err)
	}
	t.Cleanup(func() {
		// 不能用 t.Context()：用例函数返回时它已经取消，四条清理会全部
		// "context canceled" 失败，库里一轮一轮攒垃圾。
		ctx := context.Background()
		for _, sql := range []string{
			`DELETE FROM iam_role_assignment WHERE role_id=$1::uuid`,
			`DELETE FROM iam_role_permissions WHERE role_id=$1::uuid`,
			`DELETE FROM iam_role WHERE id=$1::uuid`,
		} {
			if _, err := e.pool.Exec(ctx, sql, rid.String()); err != nil {
				t.Logf("清理失败（%s）：%v", sql, err)
			}
		}
	})

	rec = e.call(admin, "POST", "/api/v1/org/roles/"+rid.String()+"/set-permissions",
		`{"permissions":["`+permID+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存角色权限返回 %d：%s", rec.Code, rec.Body.String())
	}

	// ── 3. 建一个员工（绑定登录账号），此时他还没有任何角色 ──
	uid, _ := uuid.NewV7()
	username := "rbac_rt_user_" + uid.String()
	if _, err := e.pool.Exec(t.Context(), `
		INSERT INTO accounts_user (id, password, last_login, is_superuser, username, first_name, last_name,
		  email, is_staff, is_active, date_joined, phone, nickname, organization_id, avatar, preferences)
		VALUES ($1::uuid, '!', NULL, false, $2, '', '', '', false, true, now(), '', '', NULL, NULL, '{}'::jsonb)`,
		uid.String(), username); err != nil {
		t.Fatalf("建账号失败：%v", err)
	}
	empID, _ := uuid.NewV7()
	if _, err := e.pool.Exec(t.Context(), `
		INSERT INTO iam_employee (id, created_at, updated_at, employee_no, name, phone, email,
		  id_no, position, status, user_id, organization_id)
		VALUES ($1::uuid, now(), now(), $2, $3, '', '', '', '', 'active', $4::uuid, NULL)`,
		empID.String(), "RT"+empID.String()[:8], "矩阵回环用例", uid.String()); err != nil {
		t.Fatalf("建员工档案失败：%v（表结构变了就跟着改，别改成跳过）", err)
	}
	t.Cleanup(func() {
		// 不能用 t.Context()：用例函数返回时它已经取消，四条清理会全部
		// "context canceled" 失败，库里一轮一轮攒垃圾。
		ctx := context.Background()
		for _, sql := range []string{
			`DELETE FROM iam_role_assignment WHERE user_id=$1::uuid`,
			`DELETE FROM iam_employee WHERE user_id=$1::uuid`,
			`DELETE FROM accounts_user WHERE id=$1::uuid`,
		} {
			if _, err := e.pool.Exec(ctx, sql, uid.String()); err != nil {
				t.Logf("清理失败（%s）：%v", sql, err)
			}
		}
	})
	token, _, err := e.issuer.IssuePair(uid.String())
	if err != nil {
		t.Fatalf("签发失败：%v", err)
	}

	// ── 4. 授权之前：必须是 403 ──
	// 这一步不是走过场。少了它，第 6 步的 200 可能是因为这个端点**压根没挂闸**，
	// 用例照样绿——而那正是当初财务域的真实状态。
	if rec := e.call(token, "GET", guarded, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("还没授权就能访问 %s（返回 %d）——这个端点没挂闸，"+
			"后面那句「授权后放行」证明不了任何事", guarded, rec.Code)
	}

	// ── 5. 界面上把角色发给这个人 ──
	rec = e.call(admin, "POST", "/api/v1/org/employees/"+empID.String()+"/roles",
		`{"roles":["`+rid.String()+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("分配角色返回 %d：%s", rec.Code, rec.Body.String())
	}

	// ── 6. 授权之后：同一个人、同一个端点，必须放行 ──
	if rec := e.call(token, "GET", guarded, ""); rec.Code != http.StatusOK {
		t.Fatalf("在界面上勾了 %s 并把角色发给了这个人，他访问 %s 仍然返回 %d：\n"+
			"  管理员按了保存、界面显示成功，权限却没有真正生效。\n"+
			"  查 set-permissions / employees-roles 写进去的地方，和 "+
			"auth.RolesAndPerms 读出来的地方是不是同一处。\n  响应：%s",
			wantPerm, guarded, rec.Code, rec.Body.String())
	}
}
