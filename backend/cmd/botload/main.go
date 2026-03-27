package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

type config struct {
	Host             string
	TokenFile        string
	WSURL            string
	Target           int
	BatchSize        int
	BatchInterval    time.Duration
	PingInterval     time.Duration
	ReportInterval   time.Duration
	HandshakeTimeout time.Duration
	ReconnectDelay   time.Duration
}

type stats struct {
	target           int
	started          atomic.Int64
	connectedNow     atomic.Int64
	connectedTotal   atomic.Int64
	reconnectTotal   atomic.Int64
	connectFailTotal atomic.Int64
	closeCodes       sync.Map // int -> *atomic.Int64
}

type botClient struct {
	token   string
	cfg     config
	stats   *stats
	dialer  *websocket.Dialer
	headers http.Header
}

func main() {
	cfg := parseFlags()

	tokens, err := loadTokens(cfg.TokenFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load tokens: %v\n", err)
		os.Exit(1)
	}

	cfg, err = normalizeConfig(cfg, len(tokens))
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("botload starting ws_url=%s target=%d batch_size=%d batch_interval=%s report_interval=%s\n",
		cfg.WSURL, cfg.Target, cfg.BatchSize, cfg.BatchInterval, cfg.ReportInterval)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, tokens[:cfg.Target]); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "botload failed: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	cfg := config{}
	flag.StringVar(&cfg.Host, "host", "", "host or websocket base URL for bridge connections")
	flag.StringVar(&cfg.TokenFile, "token-file", "", "path to token file, one token per line")
	flag.IntVar(&cfg.Target, "target", 0, "number of bots to start, defaults to token count")
	flag.IntVar(&cfg.BatchSize, "batch-size", 100, "number of bots to start per batch")
	flag.DurationVar(&cfg.BatchInterval, "batch-interval", 500*time.Millisecond, "delay between start batches")
	flag.DurationVar(&cfg.PingInterval, "ping-interval", 20*time.Second, "websocket ping interval to keep connections alive")
	flag.DurationVar(&cfg.ReportInterval, "report-interval", 5*time.Second, "stats report interval")
	flag.DurationVar(&cfg.HandshakeTimeout, "handshake-timeout", 10*time.Second, "websocket handshake timeout")
	flag.DurationVar(&cfg.ReconnectDelay, "reconnect-delay", 1*time.Second, "delay before reconnect")
	flag.Parse()
	return cfg
}

func normalizeConfig(cfg config, tokenCount int) (config, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return cfg, fmt.Errorf("host is required")
	}
	if strings.TrimSpace(cfg.TokenFile) == "" {
		return cfg, fmt.Errorf("token-file is required")
	}
	if tokenCount <= 0 {
		return cfg, fmt.Errorf("token file is empty")
	}
	if cfg.Target < 0 {
		return cfg, fmt.Errorf("target must be >= 0")
	}
	if cfg.Target == 0 || cfg.Target > tokenCount {
		cfg.Target = tokenCount
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = min(cfg.Target, 100)
	}
	if cfg.BatchSize > cfg.Target {
		cfg.BatchSize = cfg.Target
	}
	if cfg.BatchInterval <= 0 {
		cfg.BatchInterval = 500 * time.Millisecond
	}
	if cfg.PingInterval <= 0 {
		cfg.PingInterval = 20 * time.Second
	}
	if cfg.ReportInterval <= 0 {
		cfg.ReportInterval = 5 * time.Second
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = 10 * time.Second
	}
	if cfg.ReconnectDelay <= 0 {
		cfg.ReconnectDelay = 1 * time.Second
	}

	wsURL, err := resolveWSURL(cfg.Host)
	if err != nil {
		return cfg, err
	}
	cfg.WSURL = wsURL
	return cfg, nil
}

func resolveWSURL(host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", fmt.Errorf("empty host")
	}
	if !strings.Contains(host, "://") {
		host = "ws://" + host
	}

	u, err := url.Parse(host)
	if err != nil {
		return "", fmt.Errorf("parse host: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("host is missing in %q", host)
	}

	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}

	if u.Path == "" || u.Path == "/" {
		u.Path = "/bridge/bot"
	} else if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/bridge/bot") {
		u.Path = path.Join(u.Path, "bridge/bot")
		if !strings.HasPrefix(u.Path, "/") {
			u.Path = "/" + u.Path
		}
	}

	return u.String(), nil
}

func loadTokens(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var tokens []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		tokens = append(tokens, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return tokens, nil
}

func run(ctx context.Context, cfg config, tokens []string) error {
	stats := &stats{target: len(tokens)}
	reportDone := make(chan struct{})
	go reportLoop(ctx, stats, cfg.ReportInterval, reportDone)
	defer func() {
		<-reportDone
	}()

	dialer := &websocket.Dialer{HandshakeTimeout: cfg.HandshakeTimeout}
	for start := 0; start < len(tokens); start += cfg.BatchSize {
		end := min(start+cfg.BatchSize, len(tokens))
		for _, token := range tokens[start:end] {
			client := &botClient{
				token:  token,
				cfg:    cfg,
				stats:  stats,
				dialer: dialer,
				headers: http.Header{
					"X-Astron-Bot-Token": []string{token},
				},
			}
			stats.started.Add(1)
			go client.run(ctx)
		}

		if end < len(tokens) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(cfg.BatchInterval):
			}
		}
	}

	<-ctx.Done()
	return ctx.Err()
}

func (c *botClient) run(ctx context.Context) {
	firstConnect := true
	for {
		if ctx.Err() != nil {
			return
		}
		if !firstConnect {
			c.stats.reconnectTotal.Add(1)
			select {
			case <-ctx.Done():
				return
			case <-time.After(c.cfg.ReconnectDelay):
			}
		}
		firstConnect = false

		closeCode, err := c.connectOnce(ctx)
		if closeCode != 0 {
			c.stats.incCloseCode(closeCode)
		}
		if err != nil && ctx.Err() == nil {
			c.stats.connectFailTotal.Add(1)
		}
	}
}

func (c *botClient) connectOnce(ctx context.Context) (int, error) {
	conn, _, err := c.dialer.DialContext(ctx, c.cfg.WSURL, c.headers)
	if err != nil {
		var closeErr *websocket.CloseError
		if errors.As(err, &closeErr) {
			return closeErr.Code, err
		}
		return 0, err
	}
	defer conn.Close()

	c.stats.connectedNow.Add(1)
	c.stats.connectedTotal.Add(1)
	defer c.stats.connectedNow.Add(-1)

	pingDone := make(chan struct{})
	go c.keepAlive(ctx, conn, pingDone)
	defer func() {
		close(pingDone)
	}()

	for {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) {
				return closeErr.Code, err
			}
			return 0, err
		}
	}
}

func (c *botClient) keepAlive(ctx context.Context, conn *websocket.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(c.cfg.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			_ = conn.WriteControl(websocket.PingMessage, []byte("botload"), time.Now().Add(5*time.Second))
		}
	}
}

func reportLoop(ctx context.Context, stats *stats, interval time.Duration, done chan<- struct{}) {
	defer close(done)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	start := time.Now()
	printSummary(stats, time.Since(start))
	for {
		select {
		case <-ctx.Done():
			printSummary(stats, time.Since(start))
			return
		case <-ticker.C:
			printSummary(stats, time.Since(start))
		}
	}
}

func printSummary(stats *stats, uptime time.Duration) {
	fmt.Printf(
		"uptime=%s target=%d started=%d connected_now=%d connected_total=%d reconnect_total=%d connect_fail_total=%d close_codes=%s\n",
		uptime.Truncate(time.Second),
		stats.target,
		stats.started.Load(),
		stats.connectedNow.Load(),
		stats.connectedTotal.Load(),
		stats.reconnectTotal.Load(),
		stats.connectFailTotal.Load(),
		stats.closeCodeSummary(),
	)
}

func (s *stats) incCloseCode(code int) {
	counter, _ := s.closeCodes.LoadOrStore(code, &atomic.Int64{})
	counter.(*atomic.Int64).Add(1)
}

func (s *stats) closeCodeSummary() string {
	type pair struct {
		code  int
		count int64
	}
	var pairs []pair
	s.closeCodes.Range(func(key, value any) bool {
		pairs = append(pairs, pair{code: key.(int), count: value.(*atomic.Int64).Load()})
		return true
	})
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].code < pairs[j].code })
	if len(pairs) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, fmt.Sprintf("%d:%d", p.code, p.count))
	}
	return strings.Join(parts, ",")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
