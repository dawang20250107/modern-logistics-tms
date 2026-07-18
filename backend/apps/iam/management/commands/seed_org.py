"""组织演示数据：组织树 + 部门 + 用户组 + 员工（含汇报线）+ 服务区划。

采用典型运输企业组织结构（运输事业部→各项目部/片区），
并附经营属性、坐标、汇报线、用户组、服务区划。

幂等：以 code/employee_no 为键 update_or_create，可反复执行。
    python manage.py seed_org
"""

from django.core.management.base import BaseCommand

from apps.iam.models import (
    Department,
    Employee,
    EmployeeGroup,
    Organization,
    Permission,
    Role,
    ServiceArea,
)

# (code, name, short_name, type, property, parent_code, manager, lng, lat, city)
_ORGS = [
    ("TRANS", "运输事业部", "运输部", "group", "self", None, "符鹏", 104.066301, 30.572961, "成都市"),
    ("PRJ1", "运输项目一部", "一部", "dept", "self", "TRANS", "李涛", None, None, "成都市"),
    ("PRJ2", "运输项目二部", "二部", "dept", "self", "TRANS", "林操", None, None, "成都市"),
    ("PRJ3", "运输项目三部", "三部", "dept", "self", "TRANS", "丁健", None, None, "成都市"),
    ("PRJ4", "运输项目四部", "四部", "dept", "self", "TRANS", "杨晓丽", None, None, "成都市"),
    ("PRJ5", "运输项目五部", "五部", "dept", "self", "TRANS", "张纯豪", None, None, "成都市"),
    ("PRJ6", "运输项目六部", "六部", "dept", "self", "TRANS", "程良斌", None, None, "成都市"),
    ("WH1", "武汉片区经营1部", "武汉1", "region", "franchise", "TRANS", "皮创业", 114.305393, 30.593099, "武汉市"),
    ("WH2", "武汉片区经营2部", "武汉2", "region", "franchise", "TRANS", "朱文博", 114.305393, 30.593099, "武汉市"),
    ("SH", "上海片区经营部", "上海", "station", "partner", "TRANS", "袁明星", 121.473701, 31.230416, "上海市"),
]

_GROUPS = [
    ("OPS", "运营组"),
    ("FIN", "财务组"),
    ("DISPATCH", "调度组"),
    ("QC", "品质组"),
]

# (employee_no, name, phone, org_code, group_code, position, supervisor_no)
_EMPLOYEES = [
    ("2553728", "刘鑫", "13980678123", "TRANS", "OPS", "三图管理经理", None),
    ("2542358", "袁明星", "15102198014", "SH", "DISPATCH", "片区经理", "2553728"),
    ("2522844", "张散", "18349116097", "PRJ1", "QC", "订单品质", "2553728"),
    ("2508225", "张婷婷", "13438886114", "PRJ1", "OPS", "调度专员", "2522844"),
    ("2496670", "朱文博", "13476293270", "WH2", "DISPATCH", "片区调度", "2553728"),
    ("2496668", "皮创业", "15007135055", "WH1", "DISPATCH", "片区经理", "2553728"),
    ("2474452", "孙娟", "18908082619", "PRJ2", "OPS", "运营专员", "2553728"),
    ("2463584", "钟靖杰", "14785561693", "PRJ6", "OPS", "运营专员", "2553728"),
    ("2443517", "杨晓丽", "17713583185", "PRJ4", "FIN", "财务主管", "2553728"),
]

# (module, code, name) —— 权限点
_PERMISSIONS = [
    ("运单", "waybill.view", "查看运单"),
    ("运单", "waybill.edit", "编辑运单"),
    ("运单", "waybill.dispatch", "运单派车"),
    ("订单", "order.view", "查看订单"),
    ("订单", "order.create", "创建订单"),
    ("订单", "order.approve", "订单审批"),
    ("财务", "finance.view", "查看财务"),
    ("财务", "finance.settle", "财务结算"),
    ("组织", "org.view", "组织查看"),
    ("组织", "org.manage", "组织管理"),
    ("组织", "org.employee", "员工管理"),
    ("组织", "org.rbac", "角色权限管理"),
    ("风控", "risk.view", "风控查看"),
    ("承运商", "carrier.view", "查看承运商"),
    ("承运商", "carrier.manage", "承运商风控维护（分级/黑名单/账期）"),
    ("AI", "ai.use", "使用 AI 助手/查单"),
    ("经营分析", "analytics.view", "经营看板/指标查看"),
    ("车联网", "telematics.view", "车联网/轨迹查看"),
    ("车联网", "telematics.manage", "车联网设备/围栏管理"),
]

# (code, name, data_scope, [permission_code...]) —— 角色
_ROLES = [
    ("admin", "系统管理员", "all", ["*"]),
    ("dispatcher", "调度主管", "org_sub",
     ["waybill.view", "waybill.edit", "waybill.dispatch", "order.view", "order.create", "org.view",
      "carrier.view", "carrier.manage", "ai.use", "analytics.view", "telematics.view", "telematics.manage"]),
    ("finance", "财务专员", "org",
     ["finance.view", "finance.settle", "org.view", "carrier.view", "analytics.view"]),
    ("operator", "运营专员", "org",
     ["order.view", "order.create", "waybill.view", "org.view", "carrier.view", "ai.use",
      "analytics.view", "telematics.view"]),
]

# (org_code, area_type, region_name) —— 服务区划覆盖
_AREAS = [
    ("SH", "deliver", "上海市浦东新区"),
    ("SH", "deliver", "上海市闵行区"),
    ("SH", "transfer", "上海市嘉定区"),
    ("SH", "no_deliver", "上海市崇明区"),
    ("WH1", "deliver", "武汉市江汉区"),
    ("WH1", "transfer", "武汉市东西湖区"),
    ("WH2", "deliver", "武汉市武昌区"),
]


class Command(BaseCommand):
    help = "灌入组织中台演示数据（组织/部门/用户组/员工/服务区划），幂等可重复执行"

    def handle(self, *args, **options):
        orgs: dict[str, Organization] = {}
        for code, name, short, otype, prop, parent_code, mgr, lng, lat, city in _ORGS:
            org, _ = Organization.objects.update_or_create(
                code=code,
                defaults={
                    "name": name, "short_name": short, "type": otype, "org_property": prop,
                    "parent": orgs.get(parent_code) if parent_code else None,
                    "manager_name": mgr, "lng": lng, "lat": lat, "province": "", "city": city,
                    "is_active": True,
                },
            )
            org.save()  # 触发 path 物化
            orgs[code] = org
        self.stdout.write(f"组织 {len(orgs)} 个")

        groups: dict[str, EmployeeGroup] = {}
        for code, name in _GROUPS:
            grp, _ = EmployeeGroup.objects.update_or_create(code=code, defaults={"name": name})
            groups[code] = grp

        # 每个组织建一个默认运营部门
        depts: dict[str, Department] = {}
        for code, org in orgs.items():
            dept, _ = Department.objects.update_or_create(
                organization=org, code="DEFAULT", defaults={"name": f"{org.short_name}运营部"}
            )
            depts[code] = dept

        emps: dict[str, Employee] = {}
        # 两遍：先建员工，再挂汇报线（保证上级已存在）
        for no, name, phone, org_code, group_code, position, _sup in _EMPLOYEES:
            emp, _ = Employee.objects.update_or_create(
                employee_no=no,
                defaults={
                    "name": name, "phone": phone, "organization": orgs.get(org_code),
                    "department": depts.get(org_code), "position": position, "status": "active",
                },
            )
            emp.groups.set([groups[group_code]] if group_code in groups else [])
            emps[no] = emp
        for no, _n, _p, _o, _g, _pos, sup_no in _EMPLOYEES:
            if sup_no and sup_no in emps:
                emps[no].supervisor = emps[sup_no]
                emps[no].save(update_fields=["supervisor", "updated_at"])
        self.stdout.write(f"员工 {len(emps)} 人（含汇报线）")

        ServiceArea.objects.filter(region_name__in=[a[2] for a in _AREAS]).delete()
        for org_code, area_type, region_name in _AREAS:
            ServiceArea.objects.create(
                organization=orgs[org_code], area_type=area_type, region_name=region_name,
                city=region_name[:3], priority=10,
            )
        self.stdout.write(f"服务区划 {len(_AREAS)} 条")

        perms: dict[str, Permission] = {}
        for module, code, name in _PERMISSIONS:
            perm, _ = Permission.objects.update_or_create(
                code=code, defaults={"name": name, "module": module}
            )
            perms[code] = perm
        for code, name, scope, perm_codes in _ROLES:
            role, _ = Role.objects.update_or_create(
                code=code, defaults={"name": name, "data_scope": scope, "is_active": True}
            )
            wanted = list(perms.values()) if perm_codes == ["*"] else [
                perms[c] for c in perm_codes if c in perms
            ]
            role.permissions.set(wanted)
        self.stdout.write(f"权限点 {len(perms)} 个、角色 {len(_ROLES)} 个")

        self.stdout.write(self.style.SUCCESS("组织中台演示数据已就绪：/api/v1/org/organizations/tree"))
