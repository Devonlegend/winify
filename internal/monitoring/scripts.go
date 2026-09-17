package monitoring

import (
	"fmt"
	"strings"

	"github.com/Devonlegend/winify/internal/config"
)

// scriptFor returns the remote command that emits key=value metric lines in the
// target's native shell. Both scripts print the same keys, so parsing is shared.
func scriptFor(srv config.Server) string {
	var script string
	if srv.Type == config.ServerTypeIIS || srv.Type == config.ServerTypeWindowsService {
		script = windowsScript(srv)
	} else {
		script = linuxScript(srv)
	}
	// A Windows checkout with core.autocrlf can turn LF into CRLF inside the
	// multi-line raw string literals; strip CR so the remote shell never sees a
	// stray \r (which would corrupt paths and awk programs).
	return strings.ReplaceAll(script, "\r\n", "\n")
}

// linuxScript reads /proc and df — the same signals psutil exposes — using only
// tools present on a base Linux image (no agent, no python).
func linuxScript(srv config.Server) string {
	disk := srv.DiskPath
	if disk == "" {
		disk = "/"
	}

	var b strings.Builder
	b.WriteString(`read _ u n s i w irq sirq st _ < /proc/stat
p_idle=$((i+w))
p_total=$((u+n+s+i+w+irq+sirq+st))
sleep 1
read _ u n s i w irq sirq st _ < /proc/stat
idle=$((i+w))
total=$((u+n+s+i+w+irq+sirq+st))
dt=$((total-p_total))
di=$((idle-p_idle))
cpu=$(awk -v dt=$dt -v di=$di 'BEGIN{ if (dt<=0) print "0"; else printf "%.2f", 100*(dt-di)/dt }')
mem_total=$(awk '/^MemTotal:/{printf "%.0f", $2*1024}' /proc/meminfo)
mem_avail=$(awk '/^MemAvailable:/{printf "%.0f", $2*1024}' /proc/meminfo)
if [ -z "$mem_avail" ]; then mem_avail=$(awk '/^MemFree:/{printf "%.0f", $2*1024}' /proc/meminfo); fi
mem_used=$((mem_total - mem_avail))
set -- $(df -kP `)
	b.WriteString(shellQuote(disk))
	b.WriteString(` | awk 'NR==2{printf "%.0f %.0f", $2*1024, $3*1024}')
disk_total=$1
disk_used=$2
uptime=$(cut -d. -f1 /proc/uptime)
load1=$(cut -d' ' -f1 /proc/loadavg)
echo "cpu=$cpu"
echo "mem_total=$mem_total"
echo "mem_used=$mem_used"
echo "disk_total=$disk_total"
echo "disk_used=$disk_used"
echo "uptime=$uptime"
echo "load1=$load1"
`)
	for _, svc := range srv.Services {
		fmt.Fprintf(&b, "state=unknown\n")
		fmt.Fprintf(&b, "if command -v systemctl >/dev/null 2>&1; then state=$(systemctl is-active %s 2>/dev/null); fi\n", shellQuote(svc))
		fmt.Fprintf(&b, "if [ -z \"$state\" ]; then state=unknown; fi\n")
		fmt.Fprintf(&b, "echo %s\"$state\"\n", shellQuote("service:"+svc+"="))
	}
	return b.String()
}

// windowsScript uses Get-Counter for CPU and CIM for the rest, over the same
// PowerShell Remoting session the IIS deploy uses.
func windowsScript(srv config.Server) string {
	disk := srv.DiskPath
	if disk == "" {
		disk = "C:"
	}

	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	b.WriteString("try { $samples = (Get-Counter '\\Processor(_Total)\\% Processor Time' -SampleInterval 1 -MaxSamples 2).CounterSamples; " +
		"$cpu = [math]::Round(($samples | Measure-Object CookedValue -Average).Average, 2) } " +
		"catch { $cpu = [math]::Round((Get-CimInstance Win32_Processor | Measure-Object -Property LoadPercentage -Average).Average, 2) }\n")
	b.WriteString("$os = Get-CimInstance Win32_OperatingSystem\n")
	b.WriteString("$memTotal = [uint64]$os.TotalVisibleMemorySize * 1KB\n")
	b.WriteString("$memFree = [uint64]$os.FreePhysicalMemory * 1KB\n")
	b.WriteString("$memUsed = $memTotal - $memFree\n")
	fmt.Fprintf(&b, "$disk = Get-CimInstance Win32_LogicalDisk -Filter %s\n", psQuote("DeviceID='"+disk+"'"))
	b.WriteString("$diskTotal = [uint64]$disk.Size\n")
	b.WriteString("$diskFree = [uint64]$disk.FreeSpace\n")
	b.WriteString("$diskUsed = $diskTotal - $diskFree\n")
	b.WriteString("$uptime = [int64]((Get-Date) - $os.LastBootUpTime).TotalSeconds\n")
	b.WriteString("Write-Output \"cpu=$cpu\"\n")
	b.WriteString("Write-Output \"mem_total=$memTotal\"\n")
	b.WriteString("Write-Output \"mem_used=$memUsed\"\n")
	b.WriteString("Write-Output \"disk_total=$diskTotal\"\n")
	b.WriteString("Write-Output \"disk_used=$diskUsed\"\n")
	b.WriteString("Write-Output \"uptime=$uptime\"\n")
	b.WriteString("Write-Output \"load1=0\"\n")
	for _, svc := range srv.Services {
		fmt.Fprintf(&b, "$svc = Get-Service -Name %s -ErrorAction SilentlyContinue\n", psQuote(svc))
		fmt.Fprintf(&b, "if ($null -ne $svc) { Write-Output \"service:%s=$($svc.Status)\" } else { Write-Output \"service:%s=not-found\" }\n", svc, svc)
	}
	return b.String()
}

// shellQuote wraps s in single quotes for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// psQuote wraps s in single quotes for PowerShell.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
