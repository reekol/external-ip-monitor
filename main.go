package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	listenAddr        = ":8080"
	checkEvery        = time.Minute
	defaultHistoryMax = 10000
	defaultPageSize   = 25
)

type Config struct {
	HistoryLimit    int
	PageSizes       []int
	DefaultPageSize int
}

func loadConfig() Config {
	historyLimit := positiveIntEnv("IP_MONITOR_HISTORY_LIMIT", defaultHistoryMax)
	pageSizes := positiveIntListEnv("IP_MONITOR_PAGE_SIZES", []int{10, 25, 50, 100})
	defaultSize := positiveIntEnv("IP_MONITOR_DEFAULT_PAGE_SIZE", defaultPageSize)
	found := false
	for _, size := range pageSizes {
		if size == defaultSize {
			found = true
			break
		}
	}
	if !found {
		pageSizes = append(pageSizes, defaultSize)
	}
	return Config{HistoryLimit: historyLimit, PageSizes: pageSizes, DefaultPageSize: defaultSize}
}

func positiveIntEnv(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil || value < 1 {
		return fallback
	}
	return value
}

func positiveIntListEnv(key string, fallback []int) []int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return append([]int(nil), fallback...)
	}
	seen := make(map[int]bool)
	var values []int
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && n > 0 && !seen[n] {
			values = append(values, n)
			seen[n] = true
		}
	}
	if len(values) == 0 {
		return append([]int(nil), fallback...)
	}
	return values
}

type pageData struct {
	Snapshot
	PageSizes       []int
	DefaultPageSize int
	HistoryLimit    int
}

type Snapshot struct {
	IP          string    `json:"ip"`
	LastChecked time.Time `json:"last_checked"`
	LastError   string    `json:"last_error,omitempty"`
	ChangedAt   time.Time `json:"changed_at,omitempty"`
	History     []Event   `json:"history"`
}

type Event struct {
	IP        string    `json:"ip"`
	CheckedAt time.Time `json:"checked_at"`
	Changed   bool      `json:"changed"`
}

type Monitor struct {
	mu           sync.RWMutex
	state        Snapshot
	client       *http.Client
	logger       *log.Logger
	historyLimit int
}

func NewMonitor(logger *log.Logger, historyLimit int) *Monitor {
	if historyLimit < 1 {
		historyLimit = defaultHistoryMax
	}
	return &Monitor{
		state:        Snapshot{History: make([]Event, 0, historyLimit)},
		client:       &http.Client{Timeout: 8 * time.Second},
		logger:       logger,
		historyLimit: historyLimit,
	}
}

func (m *Monitor) Check(ctx context.Context) {
	ip, err := m.fetchExternalIP(ctx)
	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.LastChecked = now
	if err != nil {
		m.state.LastError = err.Error()
		m.logger.Printf("check failed at=%s error=%q", now.Format(time.RFC3339), err)
		return
	}

	m.state.LastError = ""
	changed := m.state.IP != "" && m.state.IP != ip
	if m.state.IP == "" || changed {
		m.state.IP = ip
		m.state.ChangedAt = now
	}
	// Keep a record for every successful check, including checks where the IP
	// did not change. The history is bounded to avoid unbounded memory use.
	m.state.History = append(m.state.History, Event{
		IP: ip, CheckedAt: now, Changed: changed,
	})
	if len(m.state.History) > m.historyLimit {
		m.state.History = append([]Event(nil), m.state.History[len(m.state.History)-m.historyLimit:]...)
	}

	if changed {
		m.logger.Printf("external IP changed ip=%s checked_at=%s", ip, now.Format(time.RFC3339))
	} else {
		m.logger.Printf("external IP check ip=%s checked_at=%s", ip, now.Format(time.RFC3339))
	}
}

func (m *Monitor) fetchExternalIP(ctx context.Context) (string, error) {
	// Try independent plain-text IP endpoints in order.
	endpoints := []string{
		"https://api.ipify.org",
		"https://checkip.amazonaws.com",
		"https://ipinfo.io/ip",
	}
	var lastErr error
	for _, endpoint := range endpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "external-ip-monitor/1.0")

		resp, err := m.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 256))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("%s returned HTTP %d", endpoint, resp.StatusCode)
			continue
		}

		ipText := strings.TrimSpace(string(body))
		parsed := net.ParseIP(ipText)
		if parsed == nil {
			lastErr = fmt.Errorf("%s returned invalid IP %q", endpoint, ipText)
			continue
		}
		return parsed.String(), nil
	}
	if lastErr == nil {
		lastErr = errors.New("no external IP endpoint available")
	}
	return "", lastErr
}

func (m *Monitor) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	copyState := m.state
	copyState.History = append([]Event(nil), m.state.History...)
	return copyState
}

var page = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>External IP Monitor</title>
<style>
:root { color-scheme: dark; --bg:#0b1020; --panel:#141c30; --line:#293650; --muted:#a6b3ca; --accent:#72e0b2; --red:#ff9b9b; }
* { box-sizing:border-box; }
html { min-height:100%; background:var(--bg); }
body { min-height:100vh; margin:0; padding:28px 16px; background:transparent; color:#eef3ff; font:16px/1.5 system-ui,sans-serif; position:relative; isolation:isolate; }
body::before { content:""; position:fixed; inset:0; z-index:-1; pointer-events:none; background:radial-gradient(ellipse at 50% 0%, #172544 0%, var(--bg) 68%); background-repeat:no-repeat; background-size:cover; }
main { max-width:850px; margin:0 auto; }
h1 { margin:0 0 6px; font-size:clamp(1.7rem,5vw,2.5rem); letter-spacing:-.04em; }
.sub { color:var(--muted); margin:0 0 24px; }
.panel { background:rgba(20,28,48,.94); border:1px solid var(--line); border-radius:18px; padding:22px; margin-bottom:16px; box-shadow:0 18px 60px #0002; }
.label { color:var(--muted); text-transform:uppercase; letter-spacing:.12em; font-size:.75rem; font-weight:700; }
.ip { font-size:clamp(2rem,8vw,4rem); line-height:1.15; font-weight:750; letter-spacing:-.045em; overflow-wrap:anywhere; margin:8px 0 14px; }
.meta { display:flex; flex-wrap:wrap; gap:8px 20px; color:var(--muted); font-size:.9rem; }
.status { display:inline-flex; align-items:center; gap:7px; color:var(--accent); }
.dot { width:8px; height:8px; background:currentColor; border-radius:50%; display:inline-block; }
.error { color:var(--red); margin-top:12px; }
table { width:100%; border-collapse:collapse; font-size:.9rem; }
th,td { text-align:left; padding:11px 8px; border-bottom:1px solid var(--line); }
th { color:var(--muted); font-weight:600; }
td:first-child { font-family:ui-monospace,monospace; overflow-wrap:anywhere; }
.badge { color:var(--accent); font-size:.78rem; }
.badge.same { color:var(--muted); }
.empty { color:var(--muted); padding:12px 0; }
footer { color:var(--muted); font-size:.8rem; text-align:center; padding:12px; }
.pager { display:flex; align-items:center; justify-content:space-between; flex-wrap:wrap; gap:10px; margin-top:16px; color:var(--muted); font-size:.85rem; }
button,select { color:#eef3ff; background:#202d47; border:1px solid #3a4a68; border-radius:9px; padding:9px 12px; font:inherit; }
button { cursor:pointer; } button:disabled { opacity:.4; cursor:not-allowed; }
.page-size { display:flex; align-items:center; gap:8px; }
@media(max-width:520px) { .panel { padding:16px; } th,td { padding:9px 5px; } .pager { justify-content:center; } }
</style>
</head>
<body>
<main>
  <h1>External IP Monitor</h1>
  <p class="sub">Public IP address · automatic check every minute</p>
  <section class="panel" aria-live="polite">
    <div class="label">Current external IP</div>
    <div class="ip" id="ip">{{if .IP}}{{.IP}}{{else}}Waiting for first check…{{end}}</div>
    <div class="meta">
      <span class="status"><span class="dot"></span><span id="status">Monitor running</span></span>
      <span>Last checked: <strong id="checked">{{if .LastChecked}}{{.LastChecked.Format "2006-01-02 15:04:05 UTC"}}{{else}}Not yet{{end}}</strong></span>
      <span>IP last changed: <strong id="changed">{{if .ChangedAt}}{{.ChangedAt.Format "2006-01-02 15:04:05 UTC"}}{{else}}Not yet{{end}}</strong></span>
    </div>
    <div class="error" id="error">{{.LastError}}</div>
  </section>
  <section class="panel">
    <div class="label">Recent checks</div>
    <table>
      <thead><tr><th>External IP</th><th>Checked at (UTC)</th><th>Result</th></tr></thead>
      <tbody id="history">
      <tr><td colspan="3" class="empty">Loading check history…</td></tr>
      </tbody>
    </table>
    <div class="pager" aria-label="History pagination">
      <button id="prevPage" type="button" disabled>Previous</button>
      <span id="pageInfo">Page 1 of 1</span>
      <button id="nextPage" type="button" disabled>Next</button>
      <label class="page-size">Rows per page
        <select id="pageSize">{{range .PageSizes}}<option value="{{.}}" {{if eq . $.DefaultPageSize}}selected{{end}}>{{.}}</option>{{end}}</select>
      </label>
    </div>
  </section>
  <footer>Showing the latest {{.HistoryLimit}} successful checks. Page status refreshes every 10 seconds; IP checks run every 60 seconds.</footer>
</main>
<script>
const fmt = value => value ? new Date(value).toISOString().replace('T',' ').replace(/\.\d{3}Z$/,' UTC') : 'Not yet';
let allHistory = [];
let currentPage = 1;
let pageSize = {{.DefaultPageSize}};
function renderHistory() {
  const body = document.getElementById('history');
  body.replaceChildren();
  const totalPages = Math.max(1, Math.ceil(allHistory.length / pageSize));
  currentPage = Math.min(currentPage, totalPages);
  const start = (currentPage - 1) * pageSize;
  const rows = allHistory.slice().reverse().slice(start, start + pageSize);
  if (rows.length === 0) {
    const tr = document.createElement('tr'), td = document.createElement('td');
    td.colSpan = 3; td.className = 'empty'; td.textContent = 'No successful checks yet.';
    tr.appendChild(td); body.appendChild(tr);
  } else {
    rows.forEach(event => {
      const tr = document.createElement('tr');
      const ip = document.createElement('td'); ip.textContent = event.ip;
      const time = document.createElement('td'); time.textContent = fmt(event.checked_at).replace(' UTC','');
      const result = document.createElement('td'), badge = document.createElement('span');
      badge.className = 'badge' + (event.changed ? '' : ' same');
      badge.textContent = event.changed ? 'IP changed' : 'Checked';
      result.appendChild(badge); tr.append(ip,time,result); body.appendChild(tr);
    });
  }
  document.getElementById('pageInfo').textContent = 'Page ' + currentPage + ' of ' + totalPages + ' · ' + allHistory.length + ' records';
  document.getElementById('prevPage').disabled = currentPage <= 1;
  document.getElementById('nextPage').disabled = currentPage >= totalPages;
}
document.getElementById('prevPage').addEventListener('click', () => { currentPage--; renderHistory(); });
document.getElementById('nextPage').addEventListener('click', () => { currentPage++; renderHistory(); });
document.getElementById('pageSize').addEventListener('change', event => {
  pageSize = Number(event.target.value) || 25;
  currentPage = 1;
  renderHistory();
});
async function refresh() {
  try {
    const response = await fetch('/api/status', {cache:'no-store'});
    if (!response.ok) throw new Error('HTTP ' + response.status);
    const data = await response.json();
    document.getElementById('ip').textContent = data.ip || 'Waiting for first check…';
    document.getElementById('checked').textContent = fmt(data.last_checked);
    document.getElementById('changed').textContent = fmt(data.changed_at);
    document.getElementById('error').textContent = data.last_error || '';
    document.getElementById('status').textContent = data.last_error ? 'Last check failed' : 'Monitor running';
    const oldCount = allHistory.length;
    allHistory = Array.isArray(data.history) ? data.history : [];
    // Keep the current page when possible; new records arrive at the end.
    if (oldCount !== allHistory.length && currentPage === 1) currentPage = 1;
    renderHistory();
  } catch (e) {
    document.getElementById('status').textContent = 'Unable to refresh page data';
  }
}
refresh();
setInterval(refresh, 10000);
</script>
</body>
</html>`))

func main() {
	logPath := strings.TrimSpace(os.Getenv("IP_MONITOR_LOG_FILE"))
	if logPath == "" {
		logPath = "ip-monitor.log"
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("open log file: %v", err)
	}
	defer logFile.Close()

	logger := log.New(io.MultiWriter(os.Stdout, logFile), "[ip-monitor] ", log.LstdFlags|log.LUTC|log.Lmicroseconds)
	cfg := loadConfig()
	monitor := NewMonitor(logger, cfg.HistoryLimit)
	logger.Printf("configuration history_limit=%d page_sizes=%v default_page_size=%d", cfg.HistoryLimit, cfg.PageSizes, cfg.DefaultPageSize)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Check immediately at startup, then once per minute.
	go func() {
		monitor.Check(ctx)
		ticker := time.NewTicker(checkEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				monitor.Check(ctx)
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := pageData{Snapshot: monitor.Snapshot(), PageSizes: cfg.PageSizes, DefaultPageSize: cfg.DefaultPageSize, HistoryLimit: cfg.HistoryLimit}
		if err := page.Execute(w, data); err != nil {
			logger.Printf("render page error: %v", err)
		}
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(monitor.Snapshot()); err != nil {
			logger.Printf("encode status error: %v", err)
		}
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		logger.Println("shutdown requested")
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Printf("graceful shutdown error: %v", err)
		}
	}()

	logger.Printf("web service listening on %s", listenAddr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("HTTP server failed: %v", err)
	}
}
