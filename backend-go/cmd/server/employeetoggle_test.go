package main

// 停用员工。
//
// 这是有人离职、或者账号被盗时按的那颗按钮。它要同时做两件事：
// 员工档案标记停用，以及**把登录账号关掉**。少做第二件，
// 界面上显示「停用」而人照样能登进来——而按下按钮的人以为已经断了。

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
)

// mkEmployeeWithAccount 造一个带登录账号的在职员工，返回员工 id 与用户名/口令。
func (e *testEnv) mkEmployeeWithAccount() (empID, username, password string) {
	e.t.Helper()
	ctx := context.Background()
	password = "Emp12345!x"
	uid := uuid.NewString()
	username = "emp_test_" + uid[:8]
	// 口令用与 seed 相同的编码方式，保证能真的登录
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO accounts_user (id, password, last_login, is_superuser, username, first_name,
		  last_name, email, is_staff, is_active, date_joined, phone, nickname, organization_id,
		  avatar, preferences)
		VALUES ($1::uuid, $2, NULL, false, $3, '', '', '', false, true, now(), '', '测试员工',
		        NULL, NULL, '{}'::jsonb)`, uid, auth.MakeDjangoPassword(password), username); err != nil {
		e.t.Fatalf("建账号失败：%v", err)
	}
	empID = uuid.NewString()
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO iam_employee (id, created_at, updated_at, employee_no, name, phone, email,
		  id_no, position, status, hire_date, department_id, organization_id, supervisor_id, user_id)
		VALUES ($1::uuid, now(), now(), $2, '测试员工', '', '', '', '专员', 'active', current_date,
		        NULL, NULL, NULL, $3::uuid)`, empID, "E"+uid[:8], uid); err != nil {
		e.t.Fatalf("建员工失败：%v", err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(ctx, `DELETE FROM iam_employee WHERE id=$1::uuid`, empID)
		_, _ = e.pool.Exec(ctx, `DELETE FROM iam_login_attempt WHERE user_id=$1::uuid`, uid)
		_, _ = e.pool.Exec(ctx, `DELETE FROM iam_token_denylist WHERE user_id=$1::uuid`, uid)
		_, _ = e.pool.Exec(ctx, `DELETE FROM accounts_user WHERE id=$1::uuid`, uid)
	})
	return empID, username, password
}

func (e *testEnv) canLogin(username, password string) bool {
	e.t.Helper()
	rec := e.call("", "POST", "/api/v1/auth/token",
		`{"username":"`+username+`","password":"`+password+`"}`)
	return rec.Code == http.StatusOK
}

// TestDisableEmployeeAlsoDisablesLogin 停用之后，人必须登不进来。
//
// 原实现两条 UPDATE 各写各的、错误都被 `_, _ =` 丢掉，也不在同一个事务里：
// 关账号那条失败时，界面照样返回 200 并显示「停用」，而人还能登录。
// 离职当天按下这颗按钮的人，不会再去验一遍。
func TestDisableEmployeeAlsoDisablesLogin(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	empID, username, password := e.mkEmployeeWithAccount()

	if !e.canLogin(username, password) {
		t.Fatal("前提不成立：新建的账号本来就登不进去")
	}

	if rec := e.call(token, "POST", "/api/v1/org/employees/"+empID+"/disable", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("停用返回 %d：%s", rec.Code, rec.Body.String())
	}

	if e.canLogin(username, password) {
		t.Error("员工已停用，账号却还能登录 —— 界面上显示「停用」，人照样进得来")
	}
	var st string
	var active bool
	_ = e.pool.QueryRow(context.Background(), `
		SELECT e.status, u.is_active FROM iam_employee e JOIN accounts_user u ON u.id = e.user_id
		WHERE e.id=$1::uuid`, empID).Scan(&st, &active)
	if st != "disabled" || active {
		t.Errorf("两条记录对不上：员工状态=%q 账号启用=%v（应为 disabled / false）", st, active)
	}
}

// TestEnableEmployeeRestoresLogin 复职要能重新登录，两条记录也要一致。
func TestEnableEmployeeRestoresLogin(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	empID, username, password := e.mkEmployeeWithAccount()

	_ = e.call(token, "POST", "/api/v1/org/employees/"+empID+"/disable", `{}`)
	if rec := e.call(token, "POST", "/api/v1/org/employees/"+empID+"/enable", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("启用返回 %d：%s", rec.Code, rec.Body.String())
	}
	if !e.canLogin(username, password) {
		t.Error("复职之后登不回来 —— 员工档案启用了，账号没跟着开")
	}
}
