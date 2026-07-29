#!/usr/bin/env bash
# 双栈契约比对已随 Django 退役而失效：没有第二个实现可对拍了。
#
# 脚本保留是为了让翻到这里的人知道它去哪了——契约验证的历史结论记在
# backend-go/PORTING.md，逐端点的比对结果与差异清单都在那份文档里。
echo "Django 上游已退役，双栈 diff 不再适用；契约结论见 backend-go/PORTING.md" >&2
exit 1
