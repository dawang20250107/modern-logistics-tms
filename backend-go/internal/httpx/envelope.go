// Package httpx 统一响应信封与错误：与 Django 侧 {success, data, error} 契约逐字节对齐，
// 前端 client.ts 无需感知后端是 Go 还是 Django。
package httpx

import (
	"encoding/json"
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
