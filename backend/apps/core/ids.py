"""UUIDv7 主键生成。

时间有序（前 48 位为毫秒时间戳），兼顾：
- 分库分表的分片友好与全局唯一；
- B-Tree 索引插入局部性（优于随机 UUIDv4）；
- 对外不暴露自增量、避免枚举。
"""

import secrets
import time
import uuid


def uuid7() -> uuid.UUID:
    ms = time.time_ns() // 1_000_000
    rand_a = secrets.randbits(12)
    rand_b = secrets.randbits(62)
    value = (ms & ((1 << 48) - 1)) << 80
    value |= 0x7 << 76          # version 7
    value |= rand_a << 64
    value |= 0b10 << 62         # variant (RFC 4122)
    value |= rand_b
    return uuid.UUID(int=value)
