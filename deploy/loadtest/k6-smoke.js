// k6 压测脚手架：JWT 登录后压运单列表 + 健康检查。
// 本地单进程 uvicorn 仅用于跑通脚本；真实容量需对水平扩展后的部署压测。
//
// 运行：
//   k6 run deploy/loadtest/k6-smoke.js
//   k6 run -e BASE_URL=http://your-host -e TMS_PASS=xxx deploy/loadtest/k6-smoke.js
import http from "k6/http";
import { check, sleep } from "k6";

const BASE = __ENV.BASE_URL || "http://127.0.0.1:8000";
const USERNAME = __ENV.TMS_USER || "admin";
const PASSWORD = __ENV.TMS_PASS || "Admin12345!";

export const options = {
  stages: [
    { duration: "30s", target: 50 },
    { duration: "1m", target: 200 },
    { duration: "30s", target: 0 },
  ],
  thresholds: {
    http_req_failed: ["rate<0.01"],
    http_req_duration: ["p(95)<500"],
  },
};

export function setup() {
  const res = http.post(
    `${BASE}/api/v1/auth/token`,
    JSON.stringify({ username: USERNAME, password: PASSWORD }),
    { headers: { "Content-Type": "application/json" } },
  );
  check(res, { "login 200": (r) => r.status === 200 });
  return { access: res.json("data.access") };
}

export default function (data) {
  const authed = { headers: { Authorization: `Bearer ${data.access}` } };
  const list = http.get(`${BASE}/api/v1/waybills?page_size=20`, authed);
  check(list, { "waybills 200": (r) => r.status === 200 });

  const health = http.get(`${BASE}/healthz`);
  check(health, { "health 200": (r) => r.status === 200 });

  sleep(1);
}
