package finance

// 财务域的权限点与拒绝文案。
//
// finance.view 这个权限点在库里一直存在、在权限矩阵界面上一直能勾，
// 但**代码里从来没有校验过**——授予它和不授予它，效果完全一样。
// 实测后果：一个刚注册、无角色无权限点无组织的账号，能读到全量应收敞口、
// 逐客户账龄、整本对账台账；POST settle 返回的是 404 而不是 403。
//
// finance.manage 是本次新增（见 auth.Catalog）：读和写必须分开，
// 会计能看不能核销，是这类系统最基本的一条职责分离。
const (
	PermView   = "finance.view"
	PermManage = "finance.manage"
)

const (
	denyView   = "无财务查看权限"
	denyManage = "无财务操作权限"
)
