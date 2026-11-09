package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// ChildEnv is a minimal environment for frpc and for verifying a configured
// kernel. Unix keeps the system tools' path; Windows keeps the inherited
// environment, which carries SystemRoot and the DLL search path.
func ChildEnv() []string {
	if runtime.GOOS == "windows" {
		return os.Environ()
	}
	return []string{"PATH=/usr/bin:/bin", "LANG=en_US.UTF-8"}
}

// DataDir is where the app keeps its state and credentials: the dot
// directory in the user's home on Unix, and %AppData%\frpc on Windows.
func DataDir() (string, error) {
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("APPDATA"); dir != "" {
			return filepath.Join(dir, "frpc"), nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".frpc"), nil
}
