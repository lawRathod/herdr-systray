package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ---------------------------------------------------------------------------
// config subcommand
// ---------------------------------------------------------------------------

func handleConfig(args []string) {
	if len(args) == 0 {
		fmt.Println(`Usage: herdr-systray config <command>

Commands:
  autostart    Manage autostart for the current user
`)
		os.Exit(1)
	}

	switch args[0] {
	case "autostart":
		handleAutostart(args[1:])
	default:
		fmt.Printf("Unknown config command: %q\n", args[0])
		os.Exit(1)
	}
}

func handleAutostart(args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "remove":
			removeAutostart()
		case "status":
			statusAutostart()
		default:
			fmt.Printf("Unknown autostart subcommand: %q\n", args[0])
			fmt.Println("Usage: herdr-systray config autostart [remove|status]")
			os.Exit(1)
		}
		return
	}

	installAutostart()
	statusAutostart()
}

// ---------------------------------------------------------------------------
// platform dispatch
// ---------------------------------------------------------------------------

func installAutostart() {
	switch runtime.GOOS {
	case "linux":
		installAutostartLinux()
	case "darwin":
		installAutostartDarwin()
	case "windows":
		installAutostartWindows()
	default:
		fmt.Fprintf(os.Stderr, "autostart not supported on %s\n", runtime.GOOS)
		os.Exit(1)
	}
}

func removeAutostart() {
	switch runtime.GOOS {
	case "linux":
		removeAutostartLinux()
	case "darwin":
		removeAutostartDarwin()
	case "windows":
		removeAutostartWindows()
	default:
		fmt.Fprintf(os.Stderr, "autostart not supported on %s\n", runtime.GOOS)
		os.Exit(1)
	}
}

func statusAutostart() {
	switch runtime.GOOS {
	case "linux":
		statusAutostartLinux()
	case "darwin":
		statusAutostartDarwin()
	case "windows":
		statusAutostartWindows()
	default:
		fmt.Fprintf(os.Stderr, "autostart not supported on %s\n", runtime.GOOS)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// Linux — XDG autostart .desktop file
// ---------------------------------------------------------------------------

const linuxAutostartFile = "herdr-systray.desktop"

func linuxAutostartPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".config", "autostart", linuxAutostartFile), nil
}

func installAutostartLinux() {
	path, err := linuxAutostartPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error resolving binary path: %v\n", err)
		os.Exit(1)
	}
	// Resolve symlinks so the entry still works after a binary swap
	resolved, err := filepath.EvalSymlinks(exe)
	if err == nil {
		exe = resolved
	}
	exe, _ = filepath.Abs(exe)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating autostart dir: %v\n", err)
		os.Exit(1)
	}

	content := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Herdr Systray
Comment=System tray monitor for Herdr coding agents
Exec=%s -d
Terminal=false
Categories=Utility;
X-GNOME-Autostart-enabled=true
`, exe)

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing autostart file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("autostart installed: %s\n", path)
	fmt.Printf("  binary: %s\n", exe)
}

func removeAutostartLinux() {
	path, err := linuxAutostartPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("autostart not installed (file does not exist)")
			return
		}
		fmt.Fprintf(os.Stderr, "error removing autostart: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("autostart removed")
}

func statusAutostartLinux() {
	path, err := linuxAutostartPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	_, err = os.Stat(path)
	if err == nil {
		data, _ := os.ReadFile(path)
		bin := ""
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "Exec=") {
				bin = strings.TrimSpace(strings.TrimPrefix(line, "Exec="))
				break
			}
		}
		fmt.Printf("autostart: installed (%s)\n", path)
		if bin != "" {
			fmt.Printf("  exec: %s\n", bin)
		}
	} else if os.IsNotExist(err) {
		fmt.Println("autostart: not installed")
	} else {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// macOS — launchd plist
// ---------------------------------------------------------------------------

const darwinLabel = "com.herdr.systray"
const darwinPlistFile = "com.herdr.systray.plist"

func darwinPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", darwinPlistFile), nil
}

func installAutostartDarwin() {
	path, err := darwinPlistPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error resolving binary path: %v\n", err)
		os.Exit(1)
	}
	exe, _ = filepath.Abs(exe)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating LaunchAgents dir: %v\n", err)
		os.Exit(1)
	}

	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>-d</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<false/>
	<key>StandardOutPath</key>
	<string>/tmp/herdr-systray.log</string>
	<key>StandardErrorPath</key>
	<string>/tmp/herdr-systray.log</string>
</dict>
</plist>
`, darwinLabel, exe)

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing plist: %v\n", err)
		os.Exit(1)
	}

	// Load into launchd
	exec.Command("launchctl", "load", path).Run()

	fmt.Printf("autostart installed: %s\n", path)
	fmt.Printf("  binary: %s\n", exe)
}

func removeAutostartDarwin() {
	path, err := darwinPlistPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Unload from launchd first
	exec.Command("launchctl", "unload", path).Run()

	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("autostart not installed")
			return
		}
		fmt.Fprintf(os.Stderr, "error removing plist: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("autostart removed")
}

func statusAutostartDarwin() {
	path, err := darwinPlistPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	_, err = os.Stat(path)
	if err == nil {
		fmt.Printf("autostart: installed (%s)\n", path)

		data, _ := os.ReadFile(path)
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "<string>/") && strings.Contains(line, "herdr") {
				fmt.Printf("  exec: %s\n", strings.TrimSpace(line))
				break
			}
		}

		// Check if loaded
		if err := exec.Command("launchctl", "list", darwinLabel).Run(); err == nil {
			fmt.Println("  loaded: yes")
		} else {
			fmt.Println("  loaded: no (run 'launchctl load' to activate)")
		}
	} else if os.IsNotExist(err) {
		fmt.Println("autostart: not installed")
	} else {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// Windows — HKCU Run registry key
// ---------------------------------------------------------------------------

func installAutostartWindows() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error resolving binary path: %v\n", err)
		os.Exit(1)
	}
	exe, _ = filepath.Abs(exe)

	// Use reg.exe to set the value — no external deps needed
	cmd := exec.Command("reg", "add",
		"HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run",
		"/v", "Herdr Systray",
		"/t", "REG_SZ",
		"/d", fmt.Sprintf(`"%s" -d`, exe),
		"/f",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "error adding registry key: %v\n%s\n", err, out)
		os.Exit(1)
	}
	fmt.Println("autostart installed (HKCU Run key)")
	fmt.Printf("  binary: %s\n", exe)
}

func removeAutostartWindows() {
	cmd := exec.Command("reg", "delete",
		"HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run",
		"/v", "Herdr Systray",
		"/f",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		// reg.exe exits 1 when the key doesn't exist
		fmt.Fprintf(os.Stderr, "error removing registry key: %v\n%s\n", err, out)
		os.Exit(1)
	}
	fmt.Println("autostart removed")
}

func statusAutostartWindows() {
	cmd := exec.Command("reg", "query",
		"HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run",
		"/v", "Herdr Systray",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		fmt.Printf("autostart: installed\n")
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "Herdr Systray") {
				fmt.Printf("  %s\n", strings.TrimSpace(line))
			}
		}
	} else {
		fmt.Println("autostart: not installed")
	}
}
