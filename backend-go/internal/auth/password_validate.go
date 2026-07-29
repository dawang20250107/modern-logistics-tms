package auth

// Django AUTH_PASSWORD_VALIDATORS 的等价实现（settings 里启用了四条内建校验器）：
//
//	UserAttributeSimilarityValidator / MinimumLengthValidator /
//	CommonPasswordValidator / NumericPasswordValidator
//
// 逐条按 django/contrib/auth/password_validation.py 复刻，包括各自的报错文案
// （项目 locale 为 zh-hans，仅 MinimumLength 的复数串未翻译，原样保留英文）。
// 常见弱口令表直接内嵌 Django 自带的 common-passwords.txt.gz（19646 条），
// 这是真实的安全控制，不能用"随便几个弱口令"的近似实现糊弄过去。

import (
	"bufio"
	"compress/gzip"
	"embed"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

//go:embed assets/common-passwords.txt.gz
var passwordAssets embed.FS

var (
	commonOnce sync.Once
	commonSet  map[string]struct{}
)

func commonPasswords() map[string]struct{} {
	commonOnce.Do(func() {
		commonSet = map[string]struct{}{}
		f, err := passwordAssets.Open("assets/common-passwords.txt.gz")
		if err != nil {
			return
		}
		defer f.Close()
		zr, err := gzip.NewReader(f)
		if err != nil {
			return
		}
		defer zr.Close()
		sc := bufio.NewScanner(zr)
		for sc.Scan() {
			if s := strings.TrimSpace(sc.Text()); s != "" {
				commonSet[strings.ToLower(s)] = struct{}{}
			}
		}
	})
	return commonSet
}

// PasswordUser 相似度校验要比对的用户属性（对应 Django 的 DEFAULT_USER_ATTRIBUTES）
type PasswordUser struct {
	Username  string
	FirstName string
	LastName  string
	Email     string
}

// nonWord 对齐 Python 的 \W+（unicode 语义：非字母/数字/下划线），
// Go 的 \W 只认 ASCII，中文姓名会被切碎，故显式写成 unicode 字符类。
var nonWord = regexp.MustCompile(`[^\p{L}\p{N}_]+`)

// quickRatio 复刻 difflib.SequenceMatcher.quick_ratio：
// 2*M/T，M 为按字符计的多重集交集大小，T 为两串长度之和。
// 注意 Django 用的正是 quick_ratio（上界估计），不是完整的 ratio()。
func quickRatio(a, b []rune) float64 {
	if len(a)+len(b) == 0 {
		return 1
	}
	cnt := map[rune]int{}
	for _, r := range b {
		cnt[r]++
	}
	matches := 0
	for _, r := range a {
		if cnt[r] > 0 {
			cnt[r]--
			matches++
		}
	}
	return 2 * float64(matches) / float64(len(a)+len(b))
}

// exceedsMaxLengthRatio 复刻同名短路函数：口令远长于比对片段时不可能超过阈值
func exceedsMaxLengthRatio(pwdLen int, maxSimilarity float64, valueLen int) bool {
	bound := maxSimilarity / 2 * float64(pwdLen)
	return pwdLen >= 10*valueLen && float64(valueLen) < bound
}

// ValidatePassword 按 Django 的顺序跑全部校验器，收集所有报错（Django 也是全跑后合并）
func ValidatePassword(password string, user *PasswordUser) []string {
	var errs []string

	// 1) UserAttributeSimilarityValidator（max_similarity=0.7）
	if user != nil {
		const maxSim = 0.7
		lowered := []rune(strings.ToLower(password))
		attrs := []struct{ value, verbose string }{
			{user.Username, "用户名"},
			{user.FirstName, "名"},
			{user.LastName, "姓"},
			{user.Email, "电子邮件地址"},
		}
		// Django 在首个命中处直接 raise，整条校验器随之结束 —— 至多一条相似度报错
	simLoop:
		for _, at := range attrs {
			if at.value == "" {
				continue
			}
			vl := strings.ToLower(at.value)
			parts := append(nonWord.Split(vl, -1), vl)
			for _, part := range parts {
				if part == "" {
					continue
				}
				pr := []rune(part)
				if exceedsMaxLengthRatio(len(lowered), maxSim, len(pr)) {
					continue
				}
				if quickRatio(lowered, pr) >= maxSim {
					errs = append(errs, fmt.Sprintf("密码跟 %s 太相似了。", at.verbose))
					break simLoop
				}
			}
		}
	}

	// 2) MinimumLengthValidator（min_length=8）——该串在 zh-hans 目录里未翻译
	if len([]rune(password)) < 8 {
		errs = append(errs, "This password is too short. It must contain at least 8 characters.")
	}

	// 3) CommonPasswordValidator
	if _, ok := commonPasswords()[strings.ToLower(strings.TrimSpace(password))]; ok {
		errs = append(errs, "这个密码太常见了。")
	}

	// 4) NumericPasswordValidator
	if password != "" && allDigits(password) {
		errs = append(errs, "密码只包含数字。")
	}
	return errs
}

func allDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
