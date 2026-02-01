// Package cache provides a file-based caching mechanism.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/adrg/xdg"
)

// Manager handles file-based caching with TTL support.
type Manager struct {
	cacheDir string
}

// Entry represents a cached item with expiry information.
type Entry struct {
	Data      json.RawMessage `json:"data"`
	CachedAt  time.Time       `json:"cached_at"`
	ExpiresAt time.Time       `json:"expires_at"`
}

// NewManager creates a new cache manager using XDG cache directory.
func NewManager() *Manager {
	return &Manager{
		cacheDir: filepath.Join(xdg.CacheHome, "llm-usage"),
	}
}

// Get retrieves a cached value if it exists and hasn't expired.
// Returns true if the cache was found and valid, false otherwise.
func (m *Manager) Get(key string, target any) (bool, error) {
	path := m.keyPath(key)

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read cache file: %w", err)
	}

	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		// Invalid cache file, treat as miss (not an error)
		return false, nil //nolint:nilerr // intentionally treat corrupt cache as miss
	}

	// Check if expired
	if time.Now().After(entry.ExpiresAt) {
		// Remove expired cache file
		_ = os.Remove(path)
		return false, nil
	}

	// Unmarshal the cached data into target
	if err := json.Unmarshal(entry.Data, target); err != nil {
		return false, fmt.Errorf("failed to unmarshal cached data: %w", err)
	}

	return true, nil
}

// Set stores a value in the cache with the given TTL.
func (m *Manager) Set(key string, data any, ttl time.Duration) error {
	if err := m.ensureCacheDir(); err != nil {
		return err
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	now := time.Now()
	entry := Entry{
		Data:      jsonData,
		CachedAt:  now,
		ExpiresAt: now.Add(ttl),
	}

	entryData, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cache entry: %w", err)
	}

	path := m.keyPath(key)
	if err := os.WriteFile(path, entryData, 0600); err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	return nil
}

// HashKey creates a cache key from a string (e.g., API key) using SHA256.
func HashKey(prefix, value string) string {
	hash := sha256.Sum256([]byte(value))
	return prefix + "_" + hex.EncodeToString(hash[:8])
}

// keyPath returns the file path for a cache key.
func (m *Manager) keyPath(key string) string {
	return filepath.Join(m.cacheDir, key+".json")
}

// ensureCacheDir creates the cache directory if it doesn't exist.
func (m *Manager) ensureCacheDir() error {
	return os.MkdirAll(m.cacheDir, 0700)
}

// Clear removes all cached files.
func (m *Manager) Clear() error {
	entries, err := os.ReadDir(m.cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read cache directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			path := filepath.Join(m.cacheDir, entry.Name())
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("failed to remove cache file %s: %w", entry.Name(), err)
			}
		}
	}

	return nil
}

// CacheDir returns the cache directory path.
func (m *Manager) CacheDir() string {
	return m.cacheDir
}
