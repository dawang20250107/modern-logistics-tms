"""地理计算与轨迹分析（纯函数，无 Django 依赖，便于单测）。"""

import math

EARTH_RADIUS_M = 6_371_000.0


def haversine_m(lng1: float, lat1: float, lng2: float, lat2: float) -> float:
    """两点球面距离（米）。"""
    p1, p2 = math.radians(lat1), math.radians(lat2)
    dphi = math.radians(lat2 - lat1)
    dlmb = math.radians(lng2 - lng1)
    a = math.sin(dphi / 2) ** 2 + math.cos(p1) * math.cos(p2) * math.sin(dlmb / 2) ** 2
    return 2 * EARTH_RADIUS_M * math.asin(math.sqrt(a))


def point_in_circle(lng: float, lat: float, center_lng: float, center_lat: float, radius_m: float) -> bool:
    return haversine_m(lng, lat, center_lng, center_lat) <= radius_m


def point_in_polygon(lng: float, lat: float, polygon: list) -> bool:
    """射线法判断点是否在多边形内。polygon: [[lng,lat], ...]。"""
    if not polygon or len(polygon) < 3:
        return False
    inside = False
    n = len(polygon)
    j = n - 1
    for i in range(n):
        xi, yi = polygon[i][0], polygon[i][1]
        xj, yj = polygon[j][0], polygon[j][1]
        if ((yi > lat) != (yj > lat)) and (lng < (xj - xi) * (lat - yi) / ((yj - yi) or 1e-12) + xi):
            inside = not inside
        j = i
    return inside


def distance_to_polyline_m(lng: float, lat: float, line: list) -> float:
    """点到折线（规划路线）的最短距离（米）。line: [[lng,lat], ...]。"""
    if not line:
        return float("inf")
    if len(line) == 1:
        return haversine_m(lng, lat, line[0][0], line[0][1])
    best = float("inf")
    for a, b in zip(line, line[1:], strict=False):
        best = min(best, _point_segment_dist_m(lng, lat, a[0], a[1], b[0], b[1]))
    return best


def _point_segment_dist_m(plng, plat, alng, alat, blng, blat) -> float:
    """点到线段距离（米），用等距投影近似（短距离足够精确）。"""
    lat0 = math.radians((alat + blat) / 2)
    mx = math.cos(lat0) * EARTH_RADIUS_M * math.pi / 180
    my = EARTH_RADIUS_M * math.pi / 180
    px, py = plng * mx, plat * my
    ax, ay = alng * mx, alat * my
    bx, by = blng * mx, blat * my
    dx, dy = bx - ax, by - ay
    seg2 = dx * dx + dy * dy
    if seg2 == 0:
        return math.hypot(px - ax, py - ay)
    t = max(0.0, min(1.0, ((px - ax) * dx + (py - ay) * dy) / seg2))
    cx, cy = ax + t * dx, ay + t * dy
    return math.hypot(px - cx, py - cy)


def analyze_trajectory(points: list, *, stop_radius_m=200.0, stop_seconds=600, speed_limit=90.0) -> dict:
    """轨迹分析：停留点 + 超速段。

    points: 已按时间升序的列表，元素需含 lng/lat/speed_kmh/reported_at（数值与 datetime）。
    返回 {stops, overspeed_segments, total_points}。
    """
    stops = []
    i = 0
    n = len(points)
    while i < n:
        j = i + 1
        while j < n and haversine_m(
            float(points[i]["lng"]), float(points[i]["lat"]),
            float(points[j]["lng"]), float(points[j]["lat"]),
        ) <= stop_radius_m:
            j += 1
        # [i, j) 为一个停留簇
        duration = (points[j - 1]["reported_at"] - points[i]["reported_at"]).total_seconds()
        if j - i >= 2 and duration >= stop_seconds:
            stops.append({
                "lng": float(points[i]["lng"]),
                "lat": float(points[i]["lat"]),
                "from": points[i]["reported_at"],
                "to": points[j - 1]["reported_at"],
                "duration_seconds": int(duration),
            })
            i = j
        else:
            i += 1

    overspeed_segments = []
    seg = None
    for p in points:
        if float(p["speed_kmh"]) > speed_limit:
            if seg is None:
                seg = {"from": p["reported_at"], "to": p["reported_at"], "max_speed": float(p["speed_kmh"])}
            else:
                seg["to"] = p["reported_at"]
                seg["max_speed"] = max(seg["max_speed"], float(p["speed_kmh"]))
        elif seg is not None:
            overspeed_segments.append(seg)
            seg = None
    if seg is not None:
        overspeed_segments.append(seg)

    return {"stops": stops, "overspeed_segments": overspeed_segments, "total_points": n}
