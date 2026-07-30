import {
  forwardRef,
  useCallback,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type ForwardedRef,
  type HTMLAttributes,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";

const VIEWPORT_GUTTER = 10;
const ANCHOR_GAP = 6;

export interface FloatingPoint {
  x: number;
  y: number;
}

export type FloatingAlign = "start" | "end";
export type FloatingAnchor = HTMLElement | DOMRectReadOnly;

export type FloatingOrigin =
  | { type: "point"; x: number; y: number }
  | { type: "anchor"; target: FloatingAnchor; align: FloatingAlign };

export function point({ x, y }: FloatingPoint): FloatingOrigin {
  return { type: "point", x, y };
}

export function anchor(target: FloatingAnchor, align: FloatingAlign = "start"): FloatingOrigin {
  return { type: "anchor", target, align };
}

export interface FloatingLayerProps extends Omit<HTMLAttributes<HTMLDivElement>, "children"> {
  origin: FloatingOrigin;
  children: ReactNode;
}

interface Position {
  left: number;
  top: number;
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), Math.max(min, max));
}

function isElementAnchor(target: FloatingAnchor): target is HTMLElement {
  return typeof HTMLElement !== "undefined" && target instanceof HTMLElement;
}

function readAnchorRect(target: FloatingAnchor): DOMRectReadOnly {
  return isElementAnchor(target) ? target.getBoundingClientRect() : target;
}

function assignRef<T>(ref: ForwardedRef<T>, value: T | null): void {
  if (typeof ref === "function") ref(value);
  else if (ref) ref.current = value;
}

export const FloatingLayer = forwardRef<HTMLDivElement, FloatingLayerProps>(function FloatingLayer(
  { origin, children, className, style, ...rest },
  forwardedRef,
) {
  const layerRef = useRef<HTMLDivElement>(null);
  const frameRef = useRef<number | null>(null);
  const [position, setPosition] = useState<Position | null>(null);

  const setLayerRef = useCallback((node: HTMLDivElement | null) => {
    layerRef.current = node;
    assignRef(forwardedRef, node);
  }, [forwardedRef]);

  useLayoutEffect(() => {
    const layer = layerRef.current;
    if (!layer) return;

    const update = () => {
      const bounds = layer.getBoundingClientRect();
      const maxLeft = window.innerWidth - VIEWPORT_GUTTER - bounds.width;
      const maxTop = window.innerHeight - VIEWPORT_GUTTER - bounds.height;

      let left: number;
      let top: number;

      if (origin.type === "point") {
        left = origin.x;
        top = origin.y;
      } else {
        const rect = readAnchorRect(origin.target);
        left = origin.align === "end" ? rect.right - bounds.width : rect.left;

        const below = rect.bottom + ANCHOR_GAP;
        const above = rect.top - ANCHOR_GAP - bounds.height;
        top = below + bounds.height <= window.innerHeight - VIEWPORT_GUTTER ? below : above;
      }

      const next = {
        left: clamp(left, VIEWPORT_GUTTER, maxLeft),
        top: clamp(top, VIEWPORT_GUTTER, maxTop),
      };
      setPosition((current) => (
        current && current.left === next.left && current.top === next.top ? current : next
      ));
    };

    const scheduleUpdate = () => {
      if (frameRef.current !== null) return;
      frameRef.current = window.requestAnimationFrame(() => {
        frameRef.current = null;
        update();
      });
    };

    update();
    window.addEventListener("resize", scheduleUpdate);
    window.addEventListener("scroll", scheduleUpdate, { capture: true, passive: true });

    const observer = new ResizeObserver(scheduleUpdate);
    observer.observe(layer);
    if (origin.type === "anchor" && isElementAnchor(origin.target)) observer.observe(origin.target);

    return () => {
      window.removeEventListener("resize", scheduleUpdate);
      window.removeEventListener("scroll", scheduleUpdate, true);
      observer.disconnect();
      if (frameRef.current !== null) {
        window.cancelAnimationFrame(frameRef.current);
        frameRef.current = null;
      }
    };
  }, [origin]);

  if (typeof document === "undefined") return null;

  const layerStyle: CSSProperties = {
    ...style,
    position: "fixed",
    left: position?.left ?? 0,
    top: position?.top ?? 0,
    visibility: position ? style?.visibility : "hidden",
  };

  return createPortal(
    <div {...rest} ref={setLayerRef} className={`floating-layer${className ? ` ${className}` : ""}`} style={layerStyle}>
      {children}
    </div>,
    document.body,
  );
});
