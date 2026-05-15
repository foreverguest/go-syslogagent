package main

import (
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var localHost string
var localIP string

func init() {
	hn, _ := os.Hostname()
	localHost = strings.ToLower(hn)
	localIP = getLocalIP()
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "127.0.0.1"
}

// ParseMessage attempts to extract date/time/host/process and build a syslog message.
func ParseMessage(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}

	// Try to find a timestamp in common formats
	date, t := findDateTime(input)

	// detect host
	//host := findHost(input)
	//if host == "" {
	//  host = localHost
	//}
	host := localHost

	// detect process
	proc := findProcess(input)
	if proc == "" {
		proc = "Unknown"
	}

	// severity default
	pri := 134 // facility local0(16)*8 + info(6) => 16*8+6=134

	// message body: include rest of line if possible, ensure single-line
	body := singleLine(sanitizeForSyslog(input))

	// format: <pri>MMM dd HH:MM:SS host proc[severity] message
	ts := time.Now().Format("Jan 2 15:04:05")
	if date != "" && t != "" {
		ts = date + " " + t
	}

	msg := strings.Builder{}
	msg.WriteString("<")
	msg.WriteString(intToString(pri))
	msg.WriteString(">")
	msg.WriteString(ts)
	msg.WriteString(" ")
	msg.WriteString(host)
	msg.WriteString(" ")
	msg.WriteString(proc)
	msg.WriteString("[info] ")
	msg.WriteString(body)

	return msg.String()
}

func intToString(i int) string { return strconv.Itoa(i) }

func sanitizeForSyslog(s string) string {
	// replace control chars (including newlines) with space
	out := strings.Map(func(r rune) rune {
		if r >= 32 {
			return r
		}
		return ' '
	}, s)
	return out
}

// singleLine collapses whitespace and removes newlines so the result is a single line
func singleLine(s string) string {
	parts := strings.Fields(s)
	return strings.Join(parts, " ")
}

func findDateTime(s string) (string, string) {
	// try ISO date and time
	iso := regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	texp := regexp.MustCompile(`\d{2}:\d{2}:\d{2}`)
	d := iso.FindString(s)
	tt := texp.FindString(s)
	if d != "" && tt != "" {
		// convert yyyy-mm-dd to "Jan _2"
		if parsed, err := time.Parse("2006-01-02", d); err == nil {
			return parsed.Format("Jan 2"), tt
		}
	}
	// fallback: time only
	if tt != "" {
		return "", tt
	}
	return "", ""
}

// func findHost(s string) string {
// 	low := strings.ToLower(s)
// 	if strings.Contains(low, localHost) {
// 		return localHost
// 	}
// 	if strings.Contains(low, localIP) {
// 		return localHost
// 	}
// 	return ""
// }

var procRe = regexp.MustCompile(`^[A-Za-z0-9\-_]{4,}`)

func findProcess(s string) string {
	s = strings.TrimSpace(s)
	m := procRe.FindString(s)
	return m
}
