// 页面级权限门：没有这个权限点就明说，而不是给一页空白。
//
// 侧栏已经按权限点过滤了（AppLayout 的 canSee），但那只挡住"点得到"。
// 直接敲地址、点收藏夹、从别人发来的链接进来的，仍然会落到页面上——
// 而页面上的接口 403 之后被前端吞掉，得到的是**一页空白：既没有数据，
// 也没有任何说明**。实测财务点进「资源库」是 157 个字符，什么都没有。
//
// 空白页最坏的地方在于它不告诉人发生了什么：用户以为是没数据、是坏了、
// 是网慢，然后去问同事、提工单。一句"你的角色没有这个权限"能省掉那一整轮。
//
// 「审计日志」那一页本来就做对了（StateView 明说"无权限或加载失败"），
// 这里把同一件事变成所有受限页面共有的。
import type { ReactNode } from "react";
import { Link } from "react-router-dom";

import { hasPerm, useAuth } from "../auth/auth";
import { StateView } from "./StateView";

export function RequirePerm({ perm, label, children }: {
  perm: string;
  /** 页面中文名，写进提示里——"你没有权限"不如"你没有查看对账中心的权限"有用 */
  label: string;
  children: ReactNode;
}) {
  const { user } = useAuth();
  // 还没取到用户时先渲染子页面：这里不是安全边界（真正的边界在后端），
  // 拦一下只是为了给出说明。抢在用户信息到位之前判，会闪一下"无权限"。
  if (!user) return <>{children}</>;
  if (hasPerm(user, perm)) return <>{children}</>;
  return (
    <StateView
      kind="empty"
      title={`没有查看「${label}」的权限`}
      hint={`这一页需要 ${perm} 权限点。请让管理员在「组织与权限 → 权限授权」里给你的角色勾上它。`}
      action={<Link className="btn-primary" to="/" style={{ textDecoration: "none" }}>返回驾驶舱</Link>}
    />
  );
}
