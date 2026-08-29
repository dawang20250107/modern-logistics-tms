// 压测驱动：闭环、有阈值、能在 CI 里当门禁跑。
//
//	go run ./cmd/loadtest -c 32 -d 30s
//	go run ./cmd/loadtest -c 32 -d 30s -p95 400 -maxerr 0.5   # 超标退出码 1
//
// 为什么不用 k6：k6 要单独装，装不上的机器（比如 CI 容器、别人的开发机）
// 就跑不了，最后变成"仓库里躺着一个没人跑过的脚本"。这个跟仓库一起编译，
// 有 Go 的地方就能跑。代价是功能比 k6 少得多，但压这套接口够用。
//
// 读的口径要说清楚，否则数字会被过度解读：
//
//   - 这是**闭环**压测：每个 worker 发一个、等一个、再发下一个。
//     服务端变慢时，客户端自动降速——也就是说它不会像开环压测那样
//     把排队时间算进延迟里。真实世界的用户不会因为你慢就少点两下。
//     所以这里的 p99 是**乐观**的，是下界不是上界。
//   - 并发数 = worker 数，不是 QPS。想要固定 QPS 得用开环模型。
//   - 只压读接口。写接口会改数据，压完一轮数据就不是原来的样子了，
//     没法重复跑；要压写得先有一套可丢弃的数据集。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type step struct {
	name   string
	path   string
	weight int // 相对权重，按真实会话里的调用频次估
}

// 一个调度员的典型会话：列表刷得最勤，详情次之，统计类偶尔看一次。
// 权重不求精确，求别把一个每天点一次的接口和列表页放在同一量级上——
// 那样压出来的数字跟线上没关系。
var mix = []step{
	{"orders.list", "/api/v1/orders?page=1&page_size=20", 10},
	{"waybills.list", "/api/v1/waybills?page=1&page_size=20", 10},
	{"orders.pool", "/api/v1/orders/pool", 6},
	{"orders.dispatched", "/api/v1/orders/dispatched", 4},
	{"waybills.stats", "/api/v1/waybills/stats", 3},
	{"orders.funnel", "/api/v1/orders/funnel", 3},
	{"finance.overview", "/api/v1/finance/statement-overview", 3},
	{"notifications.unread", "/api/v1/notifications/unread-count", 5},
	{"auth.me", "/api/v1/auth/me", 2},
	{"telematics.live", "/api/v1/telematics/vehicles/live", 2},
}

type sample struct {
	step   int
	d      time.Duration
	status int
}

func main() {
	base := flag.String("base", "http://127.0.0.1:8000", "网关地址")
	user := flag.String("user", "admin", "登录用户名")
	pass := flag.String("pass", "Admin12345!", "登录口令")
	conc := flag.Int("c", 16, "并发 worker 数")
	dur := flag.Duration("d", 20*time.Second, "压测时长")
	warm := flag.Duration("warmup", 3*time.Second, "预热时长（不计入统计）")
	p95max := flag.Float64("p95", 0, "p95 上限（毫秒），超过则退出码 1；0=不设门槛")
	maxErr := flag.Float64("maxerr", 0, "错误率上限（百分比），超过则退出码 1；0=不设门槛")
	flag.Parse()

	// 连接池必须开到并发数。Transport 默认 MaxIdleConnsPerHost=2，
	// 32 个 worker 里有 30 个每轮都要重新握手——那压的是 TCP 建连，不是接口。
	tr := &http.Transport{
		MaxIdleConns:        *conc * 2,
		MaxIdleConnsPerHost: *conc * 2,
		IdleConnTimeout:     90 * time.Second,
	}
	client := &http.Client{Transport: tr, Timeout: 30 * time.Second}

	tok, err := login(client, *base, *user, *pass)
	if err != nil {
		fmt.Fprintf(os.Stderr, "登录失败：%v\n（网关没起？口令不对？先跑 scripts/dev/up.sh）\n", err)
		os.Exit(2)
	}

	total := 0
	for _, s := range mix {
		total += s.weight
	}
	pick := make([]int, 0, total)
	for i, s := range mix {
		for j := 0; j < s.weight; j++ {
			pick = append(pick, i)
		}
	}

	fmt.Printf("目标 %s  并发 %d  预热 %s  压测 %s  接口 %d 个\n",
		*base, *conc, *warm, *dur, len(mix))

	var mu sync.Mutex
	samples := make([]sample, 0, 1<<16)
	var counting bool

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < *conc; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				idx := pick[rnd.Intn(len(pick))]
				t0 := time.Now()
				st := do(client, *base+mix[idx].path, tok)
				el := time.Since(t0)
				mu.Lock()
				if counting {
					samples = append(samples, sample{idx, el, st})
				}
				mu.Unlock()
			}
		}(time.Now().UnixNano() + int64(w))
	}

	time.Sleep(*warm)
	mu.Lock()
	counting = true
	mu.Unlock()
	start := time.Now()
	time.Sleep(*dur)
	mu.Lock()
	counting = false
	mu.Unlock()
	elapsed := time.Since(start)
	close(stop)
	wg.Wait()

	os.Exit(report(samples, elapsed, *conc, *p95max, *maxErr))
}

func login(c *http.Client, base, u, p string) (string, error) {
	body := strings.NewReader(fmt.Sprintf(`{"username":%q,"password":%q}`, u, p))
	resp, err := c.Post(base+"/api/v1/auth/token", "application/json", body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Data struct {
			Access string `json:"access"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.Data.Access == "" {
		return "", fmt.Errorf("响应里没有 access 令牌")
	}
	return out.Data.Access, nil
}

func do(c *http.Client, url, tok string) int {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.Do(req)
	if err != nil {
		return 0
	}
	// 必须读完再关，否则连接不能复用，下一轮又要握手。
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// pct 取精确分位：样本量在十万级以内，排一次序比维护近似结构简单也更准。
func pct(sorted []time.Duration, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted)-1) * p / 100)
	return float64(sorted[i].Microseconds()) / 1000
}

func report(all []sample, elapsed time.Duration, conc int, p95max, maxErr float64) int {
	if len(all) == 0 {
		fmt.Fprintln(os.Stderr, "没有采到样本")
		return 2
	}
	byStep := map[int][]time.Duration{}
	errByStep := map[int]int{}
	statusCount := map[int]int{}
	overall := make([]time.Duration, 0, len(all))
	errs := 0
	for _, s := range all {
		byStep[s.step] = append(byStep[s.step], s.d)
		overall = append(overall, s.d)
		statusCount[s.status]++
		if s.status < 200 || s.status >= 400 {
			errs++
			errByStep[s.step]++
		}
	}
	sort.Slice(overall, func(i, j int) bool { return overall[i] < overall[j] })

	rps := float64(len(all)) / elapsed.Seconds()
	errRate := float64(errs) / float64(len(all)) * 100

	fmt.Printf("\n采样 %d 次 / %.1fs   吞吐 %.0f req/s   错误 %d (%.2f%%)\n\n",
		len(all), elapsed.Seconds(), rps, errs, errRate)

	fmt.Printf("%-24s %7s %8s %8s %8s %8s %8s\n", "接口", "次数", "p50", "p90", "p95", "p99", "max")
	names := make([]int, 0, len(byStep))
	for k := range byStep {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool { return len(byStep[names[i]]) > len(byStep[names[j]]) })
	for _, k := range names {
		ds := byStep[k]
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		mark := ""
		if errByStep[k] > 0 {
			mark = fmt.Sprintf("  ✗ %d 次非 2xx/3xx", errByStep[k])
		}
		fmt.Printf("%-24s %7d %7.1f %7.1f %7.1f %7.1f %7.1f%s\n", mix[k].name, len(ds),
			pct(ds, 50), pct(ds, 90), pct(ds, 95), pct(ds, 99), pct(ds, 100), mark)
	}
	fmt.Printf("%-24s %7d %7.1f %7.1f %7.1f %7.1f %7.1f\n", "── 合计", len(overall),
		pct(overall, 50), pct(overall, 90), pct(overall, 95), pct(overall, 99), pct(overall, 100))

	codes := make([]int, 0, len(statusCount))
	for k := range statusCount {
		codes = append(codes, k)
	}
	sort.Ints(codes)
	parts := []string{}
	for _, c := range codes {
		label := fmt.Sprint(c)
		if c == 0 {
			label = "传输失败"
		}
		parts = append(parts, fmt.Sprintf("%s×%d", label, statusCount[c]))
	}
	fmt.Printf("\n状态码：%s\n", strings.Join(parts, "  "))
	fmt.Printf("口径：闭环 %d 并发，客户端会随服务端变慢而自动降速，"+
		"因此尾延迟偏乐观（是下界）。\n", conc)

	fail := 0
	if p95max > 0 {
		if got := pct(overall, 95); got > p95max {
			fmt.Printf("✗ p95 %.1fms 超过门槛 %.1fms\n", got, p95max)
			fail = 1
		} else {
			fmt.Printf("✓ p95 %.1fms ≤ %.1fms\n", got, p95max)
		}
	}
	if maxErr > 0 {
		if errRate > maxErr {
			fmt.Printf("✗ 错误率 %.2f%% 超过门槛 %.2f%%\n", errRate, maxErr)
			fail = 1
		} else {
			fmt.Printf("✓ 错误率 %.2f%% ≤ %.2f%%\n", errRate, maxErr)
		}
	}
	return fail
}
