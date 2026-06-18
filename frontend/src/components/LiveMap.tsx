import { useEffect, useRef } from "react";

import type { VehicleState } from "../api/types";
import { AMAP_KEY, useAmap } from "./useAmap";

export function LiveMap({ vehicles, height = 420 }: { vehicles: VehicleState[]; height?: number }) {
  const amap = useAmap();
  const ref = useRef<HTMLDivElement>(null);
  const mapRef = useRef<unknown>(null);
  const markersRef = useRef<unknown[]>([]);

  useEffect(() => {
    if (!amap || !ref.current) return;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const AMap = amap as any;
    if (!mapRef.current) {
      mapRef.current = new AMap.Map(ref.current, { zoom: 5, center: [104.07, 30.67] });
    }
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const map = mapRef.current as any;
    markersRef.current.forEach((m) => map.remove(m));
    markersRef.current = vehicles
      .filter((v) => Number(v.lng) && Number(v.lat))
      .map((v) => {
        const marker = new AMap.Marker({
          position: [Number(v.lng), Number(v.lat)],
          title: `${v.vehicle_plate} ${v.online ? "在线" : "离线"}`,
        });
        map.add(marker);
        return marker;
      });
  }, [amap, vehicles]);

  if (!AMAP_KEY) {
    return (
      <div className="muted small" style={{ padding: 24, textAlign: "center" }}>
        未配置高德地图 Key（VITE_AMAP_KEY），已降级为列表视图。
      </div>
    );
  }
  return <div ref={ref} style={{ width: "100%", height, borderRadius: 8 }} />;
}
