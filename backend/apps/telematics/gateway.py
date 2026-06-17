"""IoT 终端接入网关：JT/T 808 协议解析 + 上报归一化 + 入削峰队列。

- parse_jt808 / build_jt808_location：JT/T 808-2013 位置汇报(0x0200)解析与构帧（纯函数，便于测试与做模拟器）。
- normalize_terminal_message：把 JT808 帧或 JSON 上报归一化为 telemetry 上报字典。
- ingest_terminal_report：归一化后压入现有 telemetry 削峰队列，复用 flush 落库 + 报警。
"""

import json

# ── JT/T 808-2013 ───────────────────────────────────────


def _unescape(data: bytes) -> bytes:
    out = bytearray()
    i = 0
    while i < len(data):
        if data[i] == 0x7D and i + 1 < len(data):
            nxt = data[i + 1]
            if nxt == 0x01:
                out.append(0x7D)
                i += 2
                continue
            if nxt == 0x02:
                out.append(0x7E)
                i += 2
                continue
        out.append(data[i])
        i += 1
    return bytes(out)


def _escape(data: bytes) -> bytes:
    out = bytearray()
    for b in data:
        if b == 0x7E:
            out += b"\x7d\x02"
        elif b == 0x7D:
            out += b"\x7d\x01"
        else:
            out.append(b)
    return bytes(out)


def _checksum(data: bytes) -> int:
    c = 0
    for b in data:
        c ^= b
    return c


def parse_jt808(frame: bytes) -> dict:
    """解析 JT/T 808 帧；位置汇报(0x0200)额外返回经纬度/速度等。"""
    if len(frame) < 4 or frame[0] != 0x7E or frame[-1] != 0x7E:
        raise ValueError("JT808 帧定界符非法")
    inner = _unescape(frame[1:-1])
    if len(inner) < 13:
        raise ValueError("JT808 帧过短")
    payload, checksum = inner[:-1], inner[-1]
    if _checksum(payload) != checksum:
        raise ValueError("JT808 校验和不匹配")

    msg_id = int.from_bytes(payload[0:2], "big")
    props = int.from_bytes(payload[2:4], "big")
    body_len = props & 0x03FF
    phone = payload[4:10].hex()
    body = payload[12 : 12 + body_len]
    result = {"msg_id": msg_id, "terminal_phone": phone}
    if msg_id == 0x0200 and len(body) >= 28:
        result.update(_parse_location_body(body))
    return result


def _parse_location_body(body: bytes) -> dict:
    return {
        "alarm": int.from_bytes(body[0:4], "big"),
        "status": int.from_bytes(body[4:8], "big"),
        "lat": int.from_bytes(body[8:12], "big") / 1_000_000,
        "lng": int.from_bytes(body[12:16], "big") / 1_000_000,
        "altitude": int.from_bytes(body[16:18], "big"),
        "speed_kmh": int.from_bytes(body[18:20], "big") / 10.0,
        "direction": int.from_bytes(body[20:22], "big"),
        "time_bcd": body[22:28].hex(),
    }


def build_jt808_location(phone: str, lng: float, lat: float, speed_kmh: float = 0.0,
                         direction: int = 0, serial: int = 1, time_bcd: str = "260601080000") -> bytes:
    """构造一帧 0x0200 位置汇报（用于模拟器/测试）。"""
    body = (
        (0).to_bytes(4, "big")  # alarm
        + (0).to_bytes(4, "big")  # status
        + int(round(lat * 1_000_000)).to_bytes(4, "big")
        + int(round(lng * 1_000_000)).to_bytes(4, "big")
        + (0).to_bytes(2, "big")  # altitude
        + int(round(speed_kmh * 10)).to_bytes(2, "big")
        + int(direction).to_bytes(2, "big")
        + bytes.fromhex(time_bcd)
    )
    phone_bcd = bytes.fromhex(phone.rjust(12, "0"))
    header = (0x0200).to_bytes(2, "big") + (len(body) & 0x03FF).to_bytes(2, "big") + phone_bcd + serial.to_bytes(2, "big")
    payload = header + body
    framed = payload + bytes([_checksum(payload)])
    return b"\x7e" + _escape(framed) + b"\x7e"


def _bcd_time_to_iso(time_bcd: str) -> str:
    """JT808 时间 BCD(YYMMDDhhmmss, 北京时间) → ISO8601(+08:00)。"""
    if not time_bcd or len(time_bcd) < 12:
        return ""
    yy, mm, dd, hh, mi, ss = (time_bcd[i : i + 2] for i in range(0, 12, 2))
    return f"20{yy}-{mm}-{dd}T{hh}:{mi}:{ss}+08:00"


# ── 归一化 + 入队 ───────────────────────────────────────


def normalize_terminal_message(raw, *, device_no=None, vehicle_plate=None, waybill_no=None) -> dict:
    """把 JT808 帧(bytes) 或 JSON(dict/str) 上报归一化为 telemetry 上报字典。"""
    if isinstance(raw, (bytes, bytearray)):
        parsed = parse_jt808(bytes(raw))
        return {
            "device_no": device_no or parsed.get("terminal_phone"),
            "vehicle_plate": vehicle_plate,
            "waybill_no": waybill_no,
            "lng": parsed.get("lng"),
            "lat": parsed.get("lat"),
            "speed_kmh": parsed.get("speed_kmh"),
            "heading": parsed.get("direction"),
            "reported_at": _bcd_time_to_iso(parsed.get("time_bcd", "")),
            "provider": "jt808",
        }
    if isinstance(raw, str):
        raw = json.loads(raw)
    if isinstance(raw, dict):
        report = dict(raw)
        report.setdefault("provider", "mqtt")
        if device_no:
            report["device_no"] = device_no
        return report
    raise ValueError("不支持的上报类型")


def ingest_terminal_report(report: dict) -> bool:
    """把单条归一化上报压入 telemetry 削峰队列并触发异步落库。"""
    from apps.core.redis import get_redis

    from .tasks import TELEMETRY_QUEUE, flush_telemetry

    if not (report.get("device_no") or report.get("vehicle_plate")):
        return False
    get_redis().rpush(TELEMETRY_QUEUE, json.dumps(report, default=str))
    flush_telemetry.delay()
    return True
