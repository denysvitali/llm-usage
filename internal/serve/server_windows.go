//go:build windows

package serve

import (
	"os"
)

func getHomeDir() (string, error) {
	return os.UserHomeDir()
}

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
