/**
 * 日期时间选择：点击/聚焦整域即弹出原生选择器（不必精准点日历图标）。
 * type 支持 "datetime-local" | "date" | "time"。
 */
export function DateTimeField({
  value, onChange, type = "datetime-local", className = "search", style,
}: {
  value: string;
  onChange: (v: string) => void;
  type?: "datetime-local" | "date" | "time";
  className?: string;
  style?: React.CSSProperties;
}) {
  const openPicker = (el: HTMLInputElement | null) => {
    // showPicker 在现代浏览器可用；不可用时回退到默认聚焦行为。
    try { el?.showPicker?.(); } catch { /* 用户手势外调用会抛错，忽略 */ }
  };
  return (
    <input
      type={type}
      className={className}
      value={value}
      style={style}
      onChange={(e) => onChange(e.target.value)}
      onClick={(e) => openPicker(e.currentTarget)}
      onFocus={(e) => openPicker(e.currentTarget)}
    />
  );
}
