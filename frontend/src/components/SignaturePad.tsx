import { useRef } from "react";

/** 轻量电子签名画板（指针/触摸绘制），onChange 输出 dataURL。无三方依赖。 */
export function SignaturePad({ onChange, height = 140 }: { onChange: (dataUrl: string) => void; height?: number }) {
  const ref = useRef<HTMLCanvasElement>(null);
  const drawing = useRef(false);
  const last = useRef<{ x: number; y: number } | null>(null);

  const pos = (e: React.PointerEvent) => {
    const c = ref.current!;
    const r = c.getBoundingClientRect();
    return { x: ((e.clientX - r.left) / r.width) * c.width, y: ((e.clientY - r.top) / r.height) * c.height };
  };

  const start = (e: React.PointerEvent) => {
    drawing.current = true;
    last.current = pos(e);
    (e.target as HTMLCanvasElement).setPointerCapture(e.pointerId);
  };
  const move = (e: React.PointerEvent) => {
    if (!drawing.current || !ref.current) return;
    const ctx = ref.current.getContext("2d")!;
    const p = pos(e);
    ctx.strokeStyle = "#0c1320";
    ctx.lineWidth = 2.2;
    ctx.lineCap = "round";
    ctx.beginPath();
    ctx.moveTo(last.current!.x, last.current!.y);
    ctx.lineTo(p.x, p.y);
    ctx.stroke();
    last.current = p;
  };
  const end = () => {
    drawing.current = false;
    if (ref.current) onChange(ref.current.toDataURL("image/png"));
  };
  const clear = () => {
    const c = ref.current;
    if (!c) return;
    c.getContext("2d")!.clearRect(0, 0, c.width, c.height);
    onChange("");
  };

  return (
    <div>
      <canvas
        ref={ref}
        width={520}
        height={height}
        style={{ width: "100%", height, border: "1px dashed var(--line-strong)", borderRadius: 8, touchAction: "none", background: "var(--panel)" }}
        onPointerDown={start}
        onPointerMove={move}
        onPointerUp={end}
        onPointerLeave={end}
      />
      <button className="btn-ghost" style={{ marginTop: 8 }} onClick={clear}>清除签名</button>
    </div>
  );
}
