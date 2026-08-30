package auth

// 验证码下发通道。
//
// 原先这里只有一行 `fmt.Fprintf(os.Stderr, "...code=%s")`，注释写着"下发网关预留点"。
// 两个后果叠在一起，比"功能没做"更糟：
//
//   1. 生产环境用户点「忘记密码」，收不到任何东西——功能是断的；
//   2. 验证码**明文进了服务器日志**。容器日志通常汇到集中日志系统，
//      于是「能看日志」等价于「能重置任何人的密码，包括超管」。
//      这不是理论风险：运维、值班、日志平台的只读账号，本来都不该有这个能力。
//
// 现在：
//   · 没配下发通道 → /auth/password-reset/request 直接告诉用户「本系统未开通
//     自助找回，请联系管理员重置」。断了就明说，不要假装发出去了。
//   · 配了 → 走对应通道发。验证码任何情况下都不再进日志。
//
// 通道用环境变量选，实现留成接口：接短信（阿里云/腾讯云）或邮件（SMTP）
// 都只需要在这里加一个 Sender，调用点不用动。

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/smtp"
	"os"
	"strings"
)

// Sender 把验证码送到用户手上。target 是邮箱或手机号（未掩码）。
type Sender interface {
	Send(ctx context.Context, target, code string) error
	// Channel 返回通道名（email/sms），用于响应体里告诉前端"发到哪类目标了"
	Channel() string
}

// NewSender 按 TMS_RESET_CHANNEL 选通道。返回 nil 表示未开通自助找回。
//
// 刻意不做"默认回落到日志"：那正是原先的问题。没配就是没开通。
func NewSender() Sender {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TMS_RESET_CHANNEL"))) {
	case "smtp":
		s := &smtpSender{
			host: os.Getenv("SMTP_HOST"),
			port: envOr("SMTP_PORT", "587"),
			user: os.Getenv("SMTP_USER"),
			pass: os.Getenv("SMTP_PASSWORD"),
			from: envOr("SMTP_FROM", os.Getenv("SMTP_USER")),
		}
		if s.host == "" || s.from == "" {
			slog.Warn("TMS_RESET_CHANNEL=smtp 但 SMTP_HOST/SMTP_FROM 未配，自助找回按未开通处理")
			return nil
		}
		return s
	case "", "off", "none":
		return nil
	default:
		slog.Warn("未知的 TMS_RESET_CHANNEL，自助找回按未开通处理",
			"value", os.Getenv("TMS_RESET_CHANNEL"))
		return nil
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type smtpSender struct{ host, port, user, pass, from string }

func (s *smtpSender) Channel() string { return "email" }

func (s *smtpSender) Send(ctx context.Context, target, code string) error {
	if !strings.Contains(target, "@") {
		return fmt.Errorf("smtp 通道只能发邮箱，收到 %q", maskForLog(target))
	}
	msg := "From: " + s.from + "\r\n" +
		"To: " + target + "\r\n" +
		"Subject: =?UTF-8?B?" + b64("智运 TMS 密码重置验证码") + "?=\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n\r\n" +
		"你的验证码是 " + code + "，10 分钟内有效。\r\n" +
		"如果不是你本人操作，请忽略本邮件并联系管理员。\r\n"
	var authMech smtp.Auth
	if s.user != "" {
		authMech = smtp.PlainAuth("", s.user, s.pass, s.host)
	}
	return smtp.SendMail(s.host+":"+s.port, authMech, s.from, []string{target}, []byte(msg))
}

// maskForLog 出错时也不能把完整目标写进日志
func maskForLog(s string) string {
	if len(s) <= 4 {
		return "***"
	}
	return s[:2] + "***" + s[len(s)-2:]
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
