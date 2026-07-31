package auth

// 权限点规范目录 —— 「代码检查什么」与「界面能授予什么」的唯一来源。
//
// 发现的问题：`iam_permission` 表里只有 3 行（finance.view / waybill.view /
// waybill.manage），是并跑期 Django 演示数据留下的；而 Go 代码里实际会校验
// 12 个权限点。差额那 9 个（analytics.view、carrier.*、masterdata.*、org.*、
// telematics.*）在权限矩阵界面上**根本渲染不出来**——没有行就没有勾选框——
// 于是任何角色都无法被授予它们，这些功能对非超管永久 403，改配置也没用。
//
// 也就是说：RBAC 的界面、角色、分配、数据范围全都做好了，唯独目录是空的，
// 整套东西实际退化成「超管或什么都不能干」。这不是配置错误，是缺一份清单。
//
// 清单放在代码里而不是迁移 SQL 里，理由是它必须跟校验点同步演进：
// 谁加一处 Guard(..., "xxx.yyy", ...) 就必须在这里补一行，评审时一眼看得见。
// 落库走启动期幂等 upsert（EnsurePermissions），**只增不删**——
// 部署方可能自建了权限点，删除会连带 iam_role_permissions 的外键一起崩。

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Permission 一个权限点。Module 决定它在权限矩阵里归入哪一组。
type Permission struct {
	Code   string
	Name   string
	Module string
}

// Catalog 规范目录。新增校验点必须同步在此登记，否则该权限无法被授予。
//
// 命名约定：`<域>.view` 只读、`<域>.manage` 可写、少数特权单列（org.rbac）。
var Catalog = []Permission{
	// 运单
	{"waybill.view", "查看运单", "waybill"},
	{"waybill.manage", "管理运单", "waybill"},
	// 财务结算
	{"finance.view", "查看费用与对账", "finance"},
	{"finance.manage", "生成/确认/审计/核销对账单", "finance"},
	// 经营分析
	{"analytics.view", "查看经营指标", "analytics"},
	// 承运商
	{"carrier.view", "查看承运商", "carrier"},
	{"carrier.manage", "管理承运商", "carrier"},
	// 主数据（车辆/司机/客户/证件/计价规则等）
	{"masterdata.view", "查看主数据", "masterdata"},
	{"masterdata.manage", "管理主数据", "masterdata"},
	// 组织与权限
	{"org.view", "查看组织与员工", "org"},
	{"org.manage", "管理组织与员工", "org"},
	{"org.rbac", "分配角色与权限", "org"},
	// 车联网
	{"telematics.view", "查看车辆定位与轨迹", "telematics"},
	{"telematics.manage", "管理车联网设备与规则", "telematics"},
	// AI 工作台
	{"ai.use", "使用 AI 助手", "ai"},
}

// EnsurePermissions 幂等地把规范目录写入 iam_permission。
//
// 只增不删、且不覆盖已有的 name/module —— 部署方可能改过中文名，
// 启动一次就把人家的改动冲掉，是这类"自动同步"最常见的伤害方式。
func EnsurePermissions(ctx context.Context, db *pgxpool.Pool) error {
	for _, p := range Catalog {
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		if _, err := db.Exec(ctx, `
			INSERT INTO iam_permission (id, created_at, updated_at, code, name, module)
			VALUES ($1::uuid, now(), now(), $2, $3, $4)
			ON CONFLICT (code) DO NOTHING`,
			id.String(), p.Code, p.Name, p.Module); err != nil {
			return err
		}
	}
	return nil
}
