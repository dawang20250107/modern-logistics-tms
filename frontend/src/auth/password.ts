// 密码强度评估（与后端 Django 密码校验器方向一致：长度 + 多样性）。
// 返回分档、进度百分比与语义色，用于注册/改密的实时强度条。
export interface PasswordStrength {
  score: 0 | 1 | 2 | 3 | 4;
  label: string;
  pct: number;
  color: string;
  hints: string[];
}

export function passwordStrength(pwd: string): PasswordStrength {
  const hints: string[] = [];
  if (pwd.length < 8) hints.push("至少 8 位");
  let variety = 0;
  if (/[a-z]/.test(pwd)) variety++;
  if (/[A-Z]/.test(pwd)) variety++;
  if (/\d/.test(pwd)) variety++;
  if (/[^A-Za-z0-9]/.test(pwd)) variety++;
  if (variety < 3) hints.push("混合大小写 / 数字 / 符号");
  if (/^\d+$/.test(pwd)) hints.push("不要纯数字");

  let score: PasswordStrength["score"] = 0;
  if (pwd.length >= 8) {
    if (variety >= 4 && pwd.length >= 12) score = 4;
    else if (variety >= 3 && pwd.length >= 10) score = 3;
    else if (variety >= 2) score = 2;
    else score = 1;
  } else if (pwd.length > 0) {
    score = 1;
  }
  const map = {
    0: { label: "太弱", color: "var(--red)" },
    1: { label: "弱", color: "var(--red)" },
    2: { label: "一般", color: "var(--amber)" },
    3: { label: "较强", color: "var(--blue)" },
    4: { label: "很强", color: "var(--green)" },
  } as const;
  return { score, label: map[score].label, pct: (score / 4) * 100, color: map[score].color, hints };
}
