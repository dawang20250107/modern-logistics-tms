// 渲染抛错时的兜底。没有它，**整个应用会变成一张白页**。
//
// 实测（在计价规则页里人为抛一个错）：
//   页面正文 0 个字符、侧栏没了、顶栏没了、没有任何提示。
// React 在没有 error boundary 时会把整棵树卸载掉。用户看到的是一片空白，
// 他不知道可以换个地址试试（实测换一页确实能恢复），只会认为"系统挂了"。
//
// 触发它不需要多离奇的情况：一条脏数据、一个没想到的 null、一个日期解析失败，
// 任何一个渲染期抛出的异常都够。而这套系统要处理的是导进来的、
// 外部系统回写的、司机手机上传的数据。
//
// 分两层用：
//   · 路由级（AppLayout 的内容区）——出错时侧栏顶栏还在，用户点得走
//   · 应用级（App 最外层）——连布局本身崩了也还有话说
import { Component, type ErrorInfo, type ReactNode } from "react";

interface Props {
  children: ReactNode;
  /** 出错的位置，写进提示里——"页面出错了"不如"计价规则这一页出错了"有用 */
  where?: string;
  /** 路由级用：显示"返回驾驶舱"这类出口 */
  action?: ReactNode;
}
interface State {
  error: Error | null;
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // 留在控制台，出问题时截图能带走。不上报第三方——没有接。
    console.error("[渲染出错]", this.props.where ?? "", error, info.componentStack);
  }

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;
    return (
      <div className="state-empty" role="alert" style={{ padding: 32 }}>
        <div className="state-title">{this.props.where ? `「${this.props.where}」这一页出错了` : "页面出错了"}</div>
        <div className="state-hint">
          这一页没能显示出来，其他页面不受影响。可以先换一页继续；
          反馈问题时把下面这行带上，能直接定位。
        </div>
        {/* 错误原文给的是"哪一行崩了"，对排查有用；对用户也比一片空白强。
            不显示堆栈——那对用户没有意义，控制台里有。 */}
        <div className="mono small muted" style={{ marginTop: 10, wordBreak: "break-all" }}>
          {String(error.message || error)}
        </div>
        <div style={{ marginTop: 14, display: "flex", gap: 8, justifyContent: "center" }}>
          <button className="btn-primary" onClick={() => this.setState({ error: null })}>重试这一页</button>
          {this.props.action}
        </div>
      </div>
    );
  }
}
