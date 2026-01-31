// Package serve provides the HTTP server for the web UI and API
package serve

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/denysvitali/llm-usage/internal/credentials"
	"github.com/denysvitali/llm-usage/internal/provider"
	"github.com/denysvitali/llm-usage/internal/provider/claude"
	"github.com/denysvitali/llm-usage/internal/provider/kimi"
	"github.com/denysvitali/llm-usage/internal/provider/zai"
)

//go:embed web
var embeddedFS embed.FS

const (
	providerClaude = "claude"
	providerKimi   = "kimi"
	providerZAi    = "zai"
)

// Config holds the server configuration
type Config struct {
	Host   string
	Port   int
	WebDir string
}

// Server represents the HTTP server
type Server struct {
	config    *Config
	credsMgr  *credentials.Manager
	server    *http.Server
	providers []ProviderInstance
}

// ProviderInstance holds a provider instance with its account info
type ProviderInstance struct {
	provider.Provider
	AccountName string
}

// NewServer creates a new HTTP server
func NewServer(cfg *Config) *Server {
	mux := http.NewServeMux()

	s := &Server{
		config:   cfg,
		credsMgr: credentials.NewManager(),
		server: &http.Server{
			Addr:              fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		},
	}

	// Register routes
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /api/v1/usage", s.handleUsage)
	mux.HandleFunc("GET /api/v1/providers", s.handleProviders)

	return s
}

// Start starts the HTTP server
func (s *Server) Start(ctx context.Context) error {
	// Load providers on start
	s.loadProviders()

	log.Printf("Starting server on http://%s:%d", s.config.Host, s.config.Port)

	// Shutdown on context cancellation
	go func() {
		<-ctx.Done()
		log.Println("Shutting down server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.server.Shutdown(shutdownCtx)
	}()

	return s.server.ListenAndServe()
}

// loadProviders loads all configured providers
func (s *Server) loadProviders() {
	s.providers = getProviders("", "", true, s.credsMgr)
}

// getProviders returns the list of providers to query
func getProviders(providerFlag, accountFlag string, allAccounts bool, credsMgr *credentials.Manager) []ProviderInstance {
	return getProvidersWithFlags(credsMgr, accountFlag, allAccounts)
}

// getProvidersWithFlags returns providers based on filter flags
func getProvidersWithFlags(credsMgr *credentials.Manager, accountFlag string, allAccounts bool) []ProviderInstance {
	// Show all configured providers
	providerIDs := credsMgr.ListAvailable()
	if len(providerIDs) == 0 {
		providerIDs = []string{providerClaude}
	}

	var providers []ProviderInstance
	for _, pid := range providerIDs {
		switch pid {
		case providerClaude:
			// Try loading from keychain first
			keychainCreds, keychainAccount, keychainErr := loadClaudeFromKeychain()
			multiCreds, multiErr := credsMgr.LoadClaude()

			if keychainErr != nil && multiErr != nil {
				continue
			}

			if accountFlag != "" {
				if multiErr != nil {
					continue
				}
				oauth := multiCreds.GetAccount(accountFlag)
				if oauth == nil || claude.IsExpired(oauth.ExpiresAt) {
					continue
				}
				providers = append(providers, ProviderInstance{
					Provider:    claude.NewProvider(oauth.AccessToken),
					AccountName: accountFlag,
				})
				continue
			}

			// No specific account - show all available
			if keychainErr == nil && !claude.IsExpired(keychainCreds.ExpiresAt) {
				providers = append(providers, ProviderInstance{
					Provider:    claude.NewProvider(keychainCreds.AccessToken),
					AccountName: keychainAccount,
				})
			}
			if multiErr == nil {
				for _, accName := range multiCreds.ListAccounts() {
					if keychainErr == nil && accName == "default" {
						continue
					}
					oauth := multiCreds.GetAccount(accName)
					if oauth == nil || claude.IsExpired(oauth.ExpiresAt) {
						continue
					}
					providers = append(providers, ProviderInstance{
						Provider:    claude.NewProvider(oauth.AccessToken),
						AccountName: accName,
					})
				}
			}

		case providerKimi:
			creds, err := credsMgr.LoadKimi()
			if err != nil {
				continue
			}
			if allAccounts || accountFlag == "" {
				for _, accName := range creds.ListAccounts() {
					acc := creds.GetAccount(accName)
					if acc == nil {
						continue
					}
					providers = append(providers, ProviderInstance{
						Provider:    kimi.NewProvider(acc.APIKey),
						AccountName: accName,
					})
				}
			} else {
				acc := creds.GetAccount(accountFlag)
				if acc == nil {
					continue
				}
				providers = append(providers, ProviderInstance{
					Provider:    kimi.NewProvider(acc.APIKey),
					AccountName: accountFlag,
				})
			}

		case providerZAi:
			creds, err := credsMgr.LoadZAi()
			if err != nil {
				continue
			}
			if allAccounts || accountFlag == "" {
				for _, accName := range creds.ListAccounts() {
					acc := creds.GetAccount(accName)
					if acc == nil {
						continue
					}
					providers = append(providers, ProviderInstance{
						Provider:    zai.NewProvider(acc.APIKey),
						AccountName: accName,
					})
				}
			} else {
				acc := creds.GetAccount(accountFlag)
				if acc == nil {
					continue
				}
				providers = append(providers, ProviderInstance{
					Provider:    zai.NewProvider(acc.APIKey),
					AccountName: accountFlag,
				})
			}
		}
	}

	return providers
}

// loadClaudeFromKeychain tries to load Claude credentials from the CLI keychain location
func loadClaudeFromKeychain() (*credentials.OAuthCredentials, string, error) {
	homeDir, err := getHomeDir()
	if err != nil {
		return nil, "", err
	}

	credPath := homeDir + "/.claude/.credentials.json"
	data, err := readFile(credPath)
	if err != nil {
		return nil, "", err
	}

	var result struct {
		ClaudeAiOauth *credentials.OAuthCredentials `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, "", err
	}

	if result.ClaudeAiOauth == nil || result.ClaudeAiOauth.AccessToken == "" {
		return nil, "", fmt.Errorf("no valid credentials in keychain")
	}

	return result.ClaudeAiOauth, "default", nil
}

// handleIndex serves the frontend HTML
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	// First try to serve from disk (for development)
	if s.config.WebDir != "" {
		indexPath := filepath.Join(s.config.WebDir, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			http.ServeFile(w, r, indexPath)
			return
		}
	}

	// Fall back to embedded filesystem
	webFS, err := fs.Sub(embeddedFS, "web")
	if err != nil {
		http.Error(w, "Failed to load embedded files", http.StatusInternalServerError)
		return
	}

	http.FileServer(http.FS(webFS)).ServeHTTP(w, r)
}

// handleUsage returns usage statistics for all providers
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")

	// Parse query parameters
	providerFilter := r.URL.Query().Get("provider")
	accountFilter := r.URL.Query().Get("account")

	// Re-fetch providers on each request to get fresh data
	providers := s.providers
	if providerFilter != "" {
		providers = getProvidersWithFlags(s.credsMgr, accountFilter, accountFilter == "")
	}

	stats := fetchAllUsage(providers)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(stats); err != nil {
		http.Error(w, fmt.Sprintf("Error encoding JSON: %v", err), http.StatusInternalServerError)
	}
}

// handleProviders returns list of available providers
func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	type ProviderInfo struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		Accounts []string `json:"accounts"`
	}

	providerIDs := s.credsMgr.ListAvailable()
	providerList := make([]ProviderInfo, 0, len(providerIDs))

	for _, pid := range providerIDs {
		var accounts []string
		switch pid {
		case providerClaude:
			if creds, err := s.credsMgr.LoadClaude(); err == nil {
				accounts = creds.ListAccounts()
			}
			// Check for keychain credentials
			if _, _, err := loadClaudeFromKeychain(); err == nil {
				accounts = append(accounts, "default")
			}
		case providerKimi:
			if creds, err := s.credsMgr.LoadKimi(); err == nil {
				accounts = creds.ListAccounts()
			}
		case providerZAi:
			if creds, err := s.credsMgr.LoadZAi(); err == nil {
				accounts = creds.ListAccounts()
			}
		}

		name := providerName(pid)
		providerList = append(providerList, ProviderInfo{
			ID:       pid,
			Name:     name,
			Accounts: accounts,
		})
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(providerList)
}

// fetchAllUsage fetches usage from all providers concurrently
func fetchAllUsage(providers []ProviderInstance) *provider.UsageStats {
	var wg sync.WaitGroup
	var mu sync.Mutex

	stats := &provider.UsageStats{
		Providers: make([]provider.Usage, len(providers)),
	}

	for i, p := range providers {
		wg.Add(1)
		go func(idx int, prov ProviderInstance) {
			defer wg.Done()

			usage, err := prov.GetUsage()
			if err != nil {
				mu.Lock()
				stats.Providers[idx] = *provider.NewUsageError(prov.ID(), prov.Name(), err)
				mu.Unlock()
				return
			}

			if prov.AccountName != "" {
				if usage.Extra == nil {
					usage.Extra = make(map[string]any)
				}
				usage.Extra["account"] = prov.AccountName
			}

			mu.Lock()
			stats.Providers[idx] = *usage
			mu.Unlock()
		}(i, p)
	}

	wg.Wait()

	// Filter out empty providers
	var filtered []provider.Usage
	for _, p := range stats.Providers {
		if p.Provider != "" {
			filtered = append(filtered, p)
		}
	}
	stats.Providers = filtered

	return stats
}

func providerName(id string) string {
	switch id {
	case providerClaude:
		return "Claude"
	case providerKimi:
		return "Kimi"
	case providerZAi:
		return "Z.AI"
	default:
		return id
	}
}

// Command is the flag set for the serve command
type Command struct {
	Host   string
	Port   int
	WebDir string
}

// NewCommand creates a new serve command
func NewCommand(fs *flag.FlagSet) *Command {
	cmd := &Command{}
	fs.StringVar(&cmd.Host, "host", "localhost", "Host to bind to")
	fs.IntVar(&cmd.Port, "port", 8080, "Port to listen on")
	fs.StringVar(&cmd.WebDir, "web-dir", "", "Path to web directory (default: auto-detect)")
	return cmd
}

// Run executes the serve command
func (c *Command) Run(ctx context.Context) error {
	// Auto-detect web directory if not specified
	webDir := c.WebDir
	if webDir == "" {
		// Try to find the web directory relative to the executable
		if exePath, err := os.Executable(); err == nil {
			exeDir := filepath.Dir(exePath)
			candidatePaths := []string{
				filepath.Join(exeDir, "web"),
				filepath.Join(exeDir, "..", "web"),
				filepath.Join(exeDir, "..", "..", "web"),
			}
			for _, path := range candidatePaths {
				if _, err := os.Stat(path); err == nil {
					webDir = path
					break
				}
			}
		}

		// Fall back to current directory
		if webDir == "" {
			if cwd, err := os.Getwd(); err == nil {
				candidatePath := filepath.Join(cwd, "web")
				if _, err := os.Stat(candidatePath); err == nil {
					webDir = candidatePath
				}
			}
		}

		// Last resort: use ./web
		if webDir == "" {
			webDir = "./web"
		}
	}

	cfg := &Config{
		Host:   c.Host,
		Port:   c.Port,
		WebDir: webDir,
	}
	s := NewServer(cfg)
	return s.Start(ctx)
}
