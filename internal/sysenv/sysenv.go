package sysenv

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// HostInfo holds detected host runtime environment characteristics
type HostInfo struct {
	OS             string
	Kernel         string
	Arch           string
	IsRoot         bool
	PrivilegeTool  string // "doas", "sudo", "both", "none", or "root"
	PackageManager string // e.g. "apk", "apt", "pacman", "dnf", "yum", "brew", "winget", "none"
	InitSystem     string // e.g. "OpenRC", "Systemd", "Launchd", "unknown"
	Shell          string
}

var (
	cachedHostInfo *HostInfo
	hostOnce       sync.Once
)

// GetHostInfo returns cached host environment details detected dynamically
func GetHostInfo() HostInfo {
	hostOnce.Do(func() {
		info := detectHost()
		cachedHostInfo = &info
	})
	return *cachedHostInfo
}

// ResetCache resets the cached host info (primarily useful for unit tests)
func ResetCache() {
	hostOnce = sync.Once{}
	cachedHostInfo = nil
}

// detectHost inspects the runtime environment dynamically without hardcoding
func detectHost() HostInfo {
	info := HostInfo{
		Arch:  runtime.GOARCH,
		Shell: os.Getenv("SHELL"),
	}

	if runtime.GOOS == "windows" {
		info.OS = fmt.Sprintf("Windows (%s)", runtime.GOARCH)
		if info.Shell == "" {
			info.Shell = "cmd / powershell"
		}
		if _, err := exec.LookPath("winget"); err == nil {
			info.PackageManager = "winget"
		} else if _, err := exec.LookPath("choco"); err == nil {
			info.PackageManager = "choco"
		} else {
			info.PackageManager = "none"
		}
		info.PrivilegeTool = "RunAs / Administrator"
		info.InitSystem = "Windows Service Control Manager (sc.exe / net.exe)"
		return info
	}

	if runtime.GOOS == "darwin" {
		info.OS = "macOS"
		info.InitSystem = "launchd (launchctl)"
		if _, err := exec.LookPath("brew"); err == nil {
			info.PackageManager = "brew"
		}
		if _, err := exec.LookPath("sudo"); err == nil {
			info.PrivilegeTool = "sudo"
		}
		return info
	}

	// Linux & other Unix systems
	detectLinuxDistro(&info)
	detectPrivilegeTool(&info)
	detectPackageManager(&info)
	detectInitSystem(&info)

	return info
}

func detectLinuxDistro(info *HostInfo) {
	info.OS = "Linux"

	// Parse /etc/os-release or /usr/lib/os-release
	osReleasePaths := []string{"/etc/os-release", "/usr/lib/os-release"}
	for _, p := range osReleasePaths {
		f, err := os.Open(p)
		if err == nil {
			scanner := bufio.NewScanner(f)
			props := make(map[string]string)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
					continue
				}
				parts := strings.SplitN(line, "=", 2)
				key := strings.TrimSpace(parts[0])
				val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
				props[key] = val
			}
			_ = f.Close()

			if pretty, ok := props["PRETTY_NAME"]; ok && pretty != "" {
				info.OS = pretty
				break
			} else if name, ok := props["NAME"]; ok && name != "" {
				if ver, ok := props["VERSION"]; ok && ver != "" {
					info.OS = fmt.Sprintf("%s %s", name, ver)
				} else {
					info.OS = name
				}
				break
			}
		}
	}

	// Kernel version via /proc/sys/kernel/osrelease or uname
	if data, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		info.Kernel = strings.TrimSpace(string(data))
	}
}

func detectPrivilegeTool(info *HostInfo) {
	// Check if already root
	if os.Geteuid() == 0 {
		info.IsRoot = true
		info.PrivilegeTool = "root (UID 0 - already elevated, no sudo/doas needed)"
		return
	}

	hasSudo := false
	hasDoas := false

	if _, err := exec.LookPath("sudo"); err == nil {
		hasSudo = true
	}
	if _, err := exec.LookPath("doas"); err == nil {
		hasDoas = true
	}

	if hasDoas && hasSudo {
		info.PrivilegeTool = "doas / sudo (keduanya terpasang, sesuaikan dengan preferensi sistem)"
	} else if hasDoas {
		info.PrivilegeTool = "doas"
	} else if hasSudo {
		info.PrivilegeTool = "sudo"
	} else {
		info.PrivilegeTool = "none"
	}
}

func detectPackageManager(info *HostInfo) {
	mgrs := []struct {
		bin  string
		name string
		hint string
	}{
		{"apk", "apk", "apk add <pkg>, apk update"},
		{"apt-get", "apt", "apt update && apt install <pkg>"},
		{"apt", "apt", "apt update && apt install <pkg>"},
		{"pacman", "pacman", "pacman -Sy <pkg>"},
		{"dnf", "dnf", "dnf install <pkg>"},
		{"yum", "yum", "yum install <pkg>"},
		{"zypper", "zypper", "zypper install <pkg>"},
	}

	for _, m := range mgrs {
		if _, err := exec.LookPath(m.bin); err == nil {
			info.PackageManager = fmt.Sprintf("%s (misal: '%s')", m.name, m.hint)
			return
		}
	}
	info.PackageManager = "none"
}

func detectInitSystem(info *HostInfo) {
	// Check OpenRC
	if _, err := os.Stat("/run/openrc"); err == nil {
		info.InitSystem = "OpenRC (gunakan 'rc-service <nama_service> start/stop/restart/status')"
		return
	}
	if _, err := exec.LookPath("rc-service"); err == nil {
		info.InitSystem = "OpenRC (gunakan 'rc-service <nama_service> start/stop/restart/status')"
		return
	}

	// Check Systemd
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		info.InitSystem = "Systemd (gunakan 'systemctl start/stop/restart/status <nama_service>')"
		return
	}
	if _, err := exec.LookPath("systemctl"); err == nil {
		info.InitSystem = "Systemd (gunakan 'systemctl start/stop/restart/status <nama_service>')"
		return
	}

	info.InitSystem = "unknown / direct daemon"
}

// FormatPrompt generates a markdown section for the AI System Prompt
func (h HostInfo) FormatPrompt() string {
	var sb strings.Builder
	sb.WriteString("### Host System Environment (Deteksi Otomatis):\n")
	sb.WriteString(fmt.Sprintf("- OS: %s\n", h.OS))
	if h.Kernel != "" {
		sb.WriteString(fmt.Sprintf("- Kernel/Arch: %s (%s)\n", h.Kernel, h.Arch))
	} else if h.Arch != "" {
		sb.WriteString(fmt.Sprintf("- Arch: %s\n", h.Arch))
	}

	if h.IsRoot {
		sb.WriteString("- Privilege Status: Saat ini berjalan langsung sebagai root (UID 0). DILARANG menggunakan prefix sudo maupun doas.\n")
	} else if h.PrivilegeTool == "doas" {
		sb.WriteString("- Privilege Escalation: 'doas' terpasang di sistem. WAJIB gunakan 'doas' untuk perintah yang membutuhkan hak akses administrator/root. DILARANG menggunakan 'sudo' karena sudo tidak terpasang di host ini.\n")
	} else if h.PrivilegeTool == "sudo" {
		sb.WriteString("- Privilege Escalation: 'sudo' terpasang di sistem. Gunakan 'sudo' untuk perintah yang membutuhkan hak akses administrator/root.\n")
	} else if strings.Contains(h.PrivilegeTool, "doas / sudo") {
		sb.WriteString("- Privilege Escalation: 'doas' dan 'sudo' keduanya terpasang di sistem.\n")
	} else {
		sb.WriteString(fmt.Sprintf("- Privilege Tool: %s\n", h.PrivilegeTool))
	}

	if h.PackageManager != "" && h.PackageManager != "none" {
		sb.WriteString(fmt.Sprintf("- Package Manager: %s\n", h.PackageManager))
	}

	if h.InitSystem != "" && h.InitSystem != "unknown / direct daemon" {
		sb.WriteString(fmt.Sprintf("- Service Manager: %s\n", h.InitSystem))
	}

	return sb.String()
}
