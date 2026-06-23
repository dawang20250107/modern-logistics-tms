"""打卡照片水印：叠加时间 / 定位 / 节点文字。

优先用中文字体（系统 wqy/noto 等），无 CJK 字体则回退默认字体（ASCII 文本）。
Pillow 不可用时原样返回，不阻断打卡。
"""

import io

_CJK_FONT_PATHS = [
    "/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
    "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
    "/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
    "/System/Library/Fonts/PingFang.ttc",
]


def _load_font(size: int):
    from PIL import ImageFont

    for path in _CJK_FONT_PATHS:
        try:
            return ImageFont.truetype(path, size)
        except OSError:
            continue
    return ImageFont.load_default()


def watermark(image_bytes: bytes, lines: list[str]) -> bytes:
    """在图片左下角叠加半透明文字水印，返回 JPEG 字节。失败则原样返回。"""
    try:
        from PIL import Image, ImageDraw
    except Exception:  # noqa: BLE001 — 无 Pillow 时不水印
        return image_bytes
    try:
        img = Image.open(io.BytesIO(image_bytes)).convert("RGB")
    except Exception:  # noqa: BLE001 — 非图片（如 PDF）原样返回
        return image_bytes

    draw = ImageDraw.Draw(img, "RGBA")
    size = max(14, img.width // 40)
    font = _load_font(size)
    pad = size // 2
    line_h = size + pad // 2
    box_h = line_h * len(lines) + pad
    y0 = img.height - box_h
    draw.rectangle([0, y0, img.width, img.height], fill=(0, 0, 0, 110))
    y = y0 + pad // 2
    for line in lines:
        draw.text((pad, y), line, fill=(255, 255, 255, 235), font=font)
        y += line_h

    out = io.BytesIO()
    img.save(out, format="JPEG", quality=85)
    return out.getvalue()
