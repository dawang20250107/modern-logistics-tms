// Package httpx 统一响应信封与错误：与 Django 侧 {success, data, error} 契约逐字节对齐，
// 前端 client.ts 无需感知后端是 Go 还是 Django。
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

type Envelope struct {
	Success bool       `json:"success"`
	Data    any        `json:"data"`
	Error   *ErrorBody `json:"error"`
}

func JSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Envelope{Success: true, Data: data, Error: nil})
}

func Err(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Envelope{Success: false, Data: nil, Error: &ErrorBody{Code: code, Message: message}})
}

// ErrDetails 带 details 的错误信封（对齐 DRF 校验失败的 {field: [messages]} 形态）
func ErrDetails(w http.ResponseWriter, status int, code, message string, details any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Envelope{Success: false, Data: nil, Error: &ErrorBody{Code: code, Message: message, Details: details}})
}

// Micros 把计算得到的时间截断到微秒 —— Python datetime 只有微秒精度，
// Go 的 time.Now() 是纳秒。对外统一按微秒输出，两栈 wire 格式逐字节可比。
func Micros(t time.Time) time.Time { return t.Truncate(time.Microsecond) }

// Fail 服务端出错时的统一出口：**原文进日志，回给调用方的是一句人话**。
//
// 由来：62 处写成了 `httpx.Err(w, 500, "INTERNAL", "写入失败："+err.Error())`。
// 那句 err.Error() 是 Postgres 的原话，长这样：
//
//	更新失败：ERROR: invalid input syntax for type numeric: "一万" (SQLSTATE 22P02)
//
// 两个问题。一是**对用户没用**：他看不懂 SQLSTATE，也不知道该改哪一栏。
// 二是**把库的结构说出去了**：列名、列类型、约束名，有时还带上值。
// 公开下单那条路早就单独修过（不给匿名调用方看内部错误），
// 但登录之后的那 60 多处一直照抛——而"登录了"不等于"该看见数据库长什么样"，
// 客服和司机也是登录用户。
//
// 排查靠日志：这里带上 err 和调用点，日志里查得到，前端上看不到。
func Fail(w http.ResponseWriter, r *http.Request, code, userMsg string, err error) {
	slog.Error("请求处理失败",
		"code", code, "msg", userMsg, "err", err,
		"method", r.Method, "path", r.URL.Path)
	Err(w, http.StatusInternalServerError, code, userMsg)
}
