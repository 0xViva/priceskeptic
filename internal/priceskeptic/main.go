package priceskeptic

import (
	"context"
	"errors"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func Main() {
	cfg := config{
		addr:          envOr("PRICESKEPTIC_ADDR", "127.0.0.1:8080"),
		list:          envOr("PRICESKEPTIC_LIST", "handleliste.txt"),
		csvPath:       envOr("PRICESKEPTIC_CSV", "results.csv"),
		obsidian:      envOr("PRICESKEPTIC_OBSIDIAN", "/home/ajg/obsidian/August/priceskeptic.md"),
		skill:         envOr("PRICESKEPTIC_SKILL", ".opencode/skills/shopping-expert/SKILL.md"),
		opencode:      envOr("PRICESKEPTIC_OPENCODE", "opencode"),
		model:         envOr("PRICESKEPTIC_MODEL", ""),
		schedule:      envOr("PRICESKEPTIC_SCHEDULE", "10:00,22:00"),
		telegramToken: envOr("PRICESKEPTIC_TELEGRAM_TOKEN", ""),
		telegramChat:  envOr("PRICESKEPTIC_TELEGRAM_CHAT", ""),
	}
	timeoutFlag := envOr("PRICESKEPTIC_TIMEOUT", "20m")
	parallelFlag := envOr("PRICESKEPTIC_PARALLEL", "3")
	trustCache := envOr("PRICESKEPTIC_TRUST_CACHE", ".run/trustpilot.json")
	minTrustFlag := envOr("PRICESKEPTIC_MIN_TRUST", "0")
	webLogPath := envOr("PRICESKEPTIC_WEB_LOG", "websearch.log")
	postalCode := envOr("PRICESKEPTIC_POSTAL_CODE", "6773")
	requireDealDefault, err := strconv.ParseBool(envOr("PRICESKEPTIC_REQUIRE_DEAL", "true"))
	if err != nil {
		log.Fatalf("invalid PRICESKEPTIC_REQUIRE_DEAL: %v", err)
	}
	requireDeal := flag.Bool("require-deal", requireDealDefault, "drop offers without a verified sale or low price")
	backfillTrust := flag.Bool("backfill-trust", false, "refresh Trustpilot store ratings for existing rows, then exit")

	flag.StringVar(&cfg.addr, "addr", cfg.addr, "listen address")
	flag.StringVar(&cfg.list, "list", cfg.list, "shopping list file")
	flag.StringVar(&cfg.csvPath, "csv", cfg.csvPath, "results CSV file, latest 5 rows per item")
	flag.StringVar(&cfg.obsidian, "obsidian", cfg.obsidian, "Obsidian note regenerated with the results table")
	flag.StringVar(&cfg.skill, "skill", cfg.skill, "shopping-expert skill passed to opencode")
	flag.StringVar(&cfg.opencode, "opencode", cfg.opencode, "opencode executable")
	flag.StringVar(&cfg.model, "model", cfg.model, "model passed to opencode, empty for its default")
	flag.StringVar(&cfg.schedule, "schedule", cfg.schedule, "comma-separated HH:MM local times for full runs, empty disables")
	flag.StringVar(&cfg.telegramToken, "telegram-token", cfg.telegramToken, "Telegram bot token, empty disables notifications")
	flag.StringVar(&cfg.telegramChat, "telegram-chat", cfg.telegramChat, "Telegram chat ID for notifications")
	flag.StringVar(&timeoutFlag, "timeout", timeoutFlag, "per-item search timeout")
	flag.StringVar(&parallelFlag, "parallel", parallelFlag, "maximum concurrent searches")
	flag.StringVar(&trustCache, "trust-cache", trustCache, "Trustpilot rating cache file")
	flag.StringVar(&minTrustFlag, "min-trust", minTrustFlag, "drop offers from stores rated below this Trustpilot score, 0 disables")
	flag.StringVar(&webLogPath, "web-log", webLogPath, "append every fetched and searched URL to this file")
	flag.StringVar(&postalCode, "postal-code", postalCode, "buyer postal code for shipping estimates")
	flag.Parse()

	times, err := parseSchedule(cfg.schedule)
	if err != nil {
		log.Fatalf("invalid -schedule %q: %v", cfg.schedule, err)
	}
	cfg.times = times

	timeout, err := time.ParseDuration(timeoutFlag)
	if err != nil || timeout <= 0 {
		log.Fatalf("invalid -timeout %q", timeoutFlag)
	}
	cfg.timeout = timeout

	parallel, err := strconv.Atoi(parallelFlag)
	if err != nil || parallel < 1 {
		log.Fatalf("invalid -parallel %q", parallelFlag)
	}
	cfg.parallel = parallel

	minTrust, err := strconv.ParseFloat(minTrustFlag, 64)
	if err != nil || minTrust < 0 || minTrust > 5 {
		log.Fatalf("invalid -min-trust %q: want 0-5", minTrustFlag)
	}
	cfg.minTrust = minTrust
	cfg.requireDeal = *requireDeal
	cfg.trust = newTrustpilotCache(trustCache)
	cfg.webLog = newWebLog(webLogPath)
	cfg.postalCode = strings.TrimSpace(postalCode)

	absSkill, err := filepath.Abs(cfg.skill)
	if err != nil {
		log.Fatalf("resolve skill path: %v", err)
	}
	if info, err := os.Stat(absSkill); err != nil || info.IsDir() {
		log.Fatalf("skill file not found: %s", absSkill)
	}
	cfg.skill = absSkill

	store, err := openResultStore(cfg.csvPath, cfg.obsidian)
	if err != nil {
		log.Fatalf("open result store: %v", err)
	}
	if items, err := readList(cfg.list); err != nil {
		log.Printf("shopping list %s: %v", cfg.list, err)
	} else if err := store.PruneItems(items); err != nil {
		log.Printf("prune results: %v", err)
	}

	root, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *backfillTrust {
		filled, total := store.BackfillTrust(root, cfg.trust)
		if err := store.Close(); err != nil {
			log.Fatalf("close result store: %v", err)
		}
		log.Printf("trustpilot: updated %d of %d rows", filled, total)
		return
	}

	var notify notifier
	if n := newTelegramNotifier(cfg.telegramToken, cfg.telegramChat); n != nil {
		notify = n
		log.Print("telegram: notifications enabled")
	}

	s := &server{
		cfg:     cfg,
		results: store,
		notify:  notify,
		sem:     make(chan struct{}, cfg.parallel),
		root:    root,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("GET /results", s.handleResults)
	mux.HandleFunc("POST /search", s.handleSearch)
	mux.HandleFunc("POST /notify", s.handleNotify)

	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-root.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	go func() {
		select {
		case <-time.After(2 * time.Second):
			if _, _, err := s.startRun("startup"); err != nil {
				log.Printf("startup run: %v", err)
			}
		case <-root.Done():
		}
	}()
	if len(cfg.times) > 0 {
		go s.runScheduler()
	}

	log.Printf("priceskeptic: listening on %s, list %s, csv %s, obsidian %s, schedule %q", cfg.addr, cfg.list, cfg.csvPath, cfg.obsidian, cfg.schedule)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		store.Close()
		log.Fatal(err)
	}
	log.Print("priceskeptic: stopped")
	store.Close()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
