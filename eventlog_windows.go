//go:build windows

package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// StartEventLogPoller starts a goroutine that polls one or more Windows Event Logs using wevtutil
// and forwards new entries to the syslog server. It stores last processed timestamp
// in registry value 'LastEventPollTime' under SOFTWARE\Syslog Agent as RFC3339.
func StartEventLogPoller(logNames []string, intervalSeconds int, server string) {
	go func() {
		// load persisted processed cache from disk
		if err := loadProcessed(); err != nil {
			LogWarn("failed loading processed cache: %v", err)
		}
		// start save scheduler to batch save requests
		startSaveScheduler()
		ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
		defer ticker.Stop()

		// Load last time
		lastTimeStr, _ := ReadRegistryTime("LastEventPollTime")
		var lastTime time.Time
		if lastTimeStr != "" {
			if t, err := time.Parse(time.RFC3339, lastTimeStr); err == nil {
				lastTime = t
			}
		}

		for {
			// aggregate fragments per EventRecordID for a short window
			newest := lastTime
			for _, logName := range logNames {
				// query last N events from specified log in XML
				out, err := exec.Command("wevtutil", "qe", logName, "/rd:true", "/f:xml", "/c:100").Output()
				if err != nil {
					LogWarn("wevtutil read failed for %s: %v", logName, err)
					continue
				}
				entries := parseWevtutilOutputXML(out, logName)
				// entries are newest-first; process oldest-first
				for i := len(entries) - 1; i >= 0; i-- {
					e := entries[i]
					// skip if we've already processed this EventRecordID recently
					skip := false
					processedMu.Lock()
					if t, ok := processed[e.Unique]; ok {
						if time.Since(t) < processedRetention {
							skip = true
						} else {
							delete(processed, e.Unique)
						}
					}
					if !skip {
						// mark processed to avoid re-sending while aggregating
						processed[e.Unique] = time.Now()
						// schedule batch save
						requestSaveProcessed()
					}
					processedMu.Unlock()
					if skip {
						Debug("Skipping duplicate event %s from %s", e.Unique, logName)
						continue
					}
					Debug("Found event %s from %s at %s: %s", e.Unique, logName, e.Time.Format(time.RFC3339), singleLine(e.Message))
					// schedule aggregation and eventual send
					addPendingAndSchedule(e.Unique, e.Message, e.Time, e.Provider, e.Computer, server)
					if e.Time.After(newest) {
						newest = e.Time
					}
				}

			}

			if !newest.IsZero() && newest.After(lastTime) {
				lastTime = newest
				_ = WriteRegistryTime("LastEventPollTime", lastTime.Format(time.RFC3339))
			}

			<-ticker.C
		}
	}()
}

type wevtEntry struct {
	Time     time.Time
	Computer string
	Provider string
	Message  string
	Unique   string // logName:EventRecordID
	LogName  string
}

// aggregator to collect partitioned messages for same EventRecordID
type pendingEntry struct {
	parts    []string
	timer    *time.Timer
	firstTS  time.Time
	provider string
	computer string
}

var (
	pendingMu   sync.Mutex
	pending     = make(map[string]*pendingEntry)
	processedMu sync.Mutex
	processed   = make(map[string]time.Time) // unique -> time processed
	saveRequest = make(chan struct{}, 1)
)

const aggregateWindow = 500 * time.Millisecond
const processedRetention = 2 * time.Hour

// (no periodic cleanup timer: cleanup is performed before saving)
const saveDebounceInterval = 5 * time.Second

func addPendingAndSchedule(unique, part string, ts time.Time, provider, computer, server string) {
	pendingMu.Lock()
	pe, ok := pending[unique]
	if !ok {
		pe = &pendingEntry{parts: []string{}, firstTS: ts, provider: provider, computer: computer}
		pending[unique] = pe
	}
	pe.parts = append(pe.parts, part)
	if pe.timer != nil {
		pe.timer.Stop()
	}
	// schedule flush after aggregateWindow
	pe.timer = time.AfterFunc(aggregateWindow, func() {
		flushPending(unique, server)
	})
	pendingMu.Unlock()
}

// requestSaveProcessed signals the save scheduler to batch and persist the processed cache
func requestSaveProcessed() {
	select {
	case saveRequest <- struct{}{}:
	default:
		// already a pending request
	}
}

func startSaveScheduler() {
	// non-blocking; if already started, this creates another scheduler but that's harmless
	go func() {
		for {
			// wait for first request
			<-saveRequest
			timer := time.NewTimer(saveDebounceInterval)
			for {
				select {
				case <-saveRequest:
					// reset timer
					if !timer.Stop() {
						<-timer.C
					}
					timer.Reset(saveDebounceInterval)
				case <-timer.C:
					// perform cleanup and save
					cleanupProcessed()
					if err := saveProcessed(); err != nil {
						LogWarn("failed saving processed cache: %v", err)
					}
					goto WAIT
				}
			}
		WAIT:
			continue
		}
	}()
}

func flushPending(unique string, server string) {
	pendingMu.Lock()
	pe, ok := pending[unique]
	if !ok {
		pendingMu.Unlock()
		return
	}
	delete(pending, unique)
	pendingMu.Unlock()

	// combine parts with a space
	combined := strings.Join(pe.parts, " ")
	// build a wevtEntry-like object to reuse buildSyslogFromEvent
	e := wevtEntry{Message: combined, Time: pe.firstTS, Provider: pe.provider, Computer: pe.computer, LogName: ""}
	// unique is formatted as logName:EventRecordID
	if before, _, ok0 := strings.Cut(unique, ":"); ok0 {
		e.LogName = before
	}
	msg := buildSyslogFromEvent(e)
	Debug("Sending event %s to %s: %s", unique, server, msg)
	_ = SendSyslog(msg, server)
}

// parseWevtutilOutputXML parses XML output from wevtutil and returns entries
func parseWevtutilOutputXML(out []byte, logName string) []wevtEntry {
	type TimeCreated struct {
		SystemTime string `xml:"SystemTime,attr"`
	}
	type System struct {
		Provider struct {
			Name string `xml:"Name,attr"`
		} `xml:"Provider"`
		TimeCreated   TimeCreated `xml:"TimeCreated"`
		Computer      string      `xml:"Computer"`
		EventRecordID struct {
			ID string `xml:",chardata"`
		} `xml:"EventRecordID"`
	}
	type Event struct {
		XMLName       xml.Name `xml:"Event"`
		System        System   `xml:"System"`
		RenderingInfo struct {
			Message string `xml:"Message" xml:",innerxml"`
		} `xml:"RenderingInfo"`
	}

	s := string(out)
	var entries []wevtEntry
	// naive split: find <Event ...>...</Event> blocks
	startIdx := 0
	for {
		si := strings.Index(s[startIdx:], "<Event")
		if si == -1 {
			break
		}
		si += startIdx
		ei := strings.Index(s[si:], "</Event>")
		if ei == -1 {
			break
		}
		ei += si + len("</Event>")
		block := s[si:ei]

		var ev Event
		if err := xml.Unmarshal([]byte(block), &ev); err == nil {
			var e wevtEntry
			e.Provider = ev.System.Provider.Name
			// parse time (try multiple layouts)
			ts := strings.TrimSpace(ev.System.TimeCreated.SystemTime)
			if ts != "" {
				if t, err := time.Parse(time.RFC3339, ts); err == nil {
					e.Time = t
				} else if t2, err2 := time.Parse(time.RFC3339Nano, ts); err2 == nil {
					e.Time = t2
				} else {
					// try common wevtutil layout with fractional seconds
					if t3, err3 := time.Parse("2006-01-02T15:04:05.0000000Z07:00", ts); err3 == nil {
						e.Time = t3
					} else {
						// fallback to now so events advance
						e.Time = time.Now()
					}
				}
			} else {
				e.Time = time.Now()
			}
			e.Computer = ev.System.Computer
			// try rendered message first
			msg := strings.TrimSpace(ev.RenderingInfo.Message)
			// if empty, try extracting inner RenderingInfo text from raw block
			if msg == "" {
				// find RenderingInfo block in raw XML
				rsi := strings.Index(block, "<RenderingInfo")
				if rsi != -1 {
					rei := strings.Index(block[rsi:], "</RenderingInfo>")
					if rei != -1 {
						rei += rsi + len("</RenderingInfo>")
						riBlock := block[rsi:rei]
						// strip tags to get plain text
						re := regexp.MustCompile(`<[^>]+>`)
						stripped := re.ReplaceAllString(riBlock, " ")
						msg = strings.TrimSpace(html.UnescapeString(stripped))
					}
				}
			}
			// fallback: whole block
			if msg == "" {
				re := regexp.MustCompile(`<[^>]+>`)
				stripped := re.ReplaceAllString(block, " ")
				msg = strings.TrimSpace(html.UnescapeString(stripped))
			}
			e.Message = msg
			// unique id: prefer EventRecordID when present, otherwise group by provider+time+computer
			id := strings.TrimSpace(ev.System.EventRecordID.ID)
			if id != "" {
				e.Unique = fmt.Sprintf("%s:%s", logName, id)
			} else {
				e.Unique = fmt.Sprintf("%s:%s:%s:%s", logName, strings.TrimSpace(ev.System.Provider.Name), ts, strings.TrimSpace(ev.System.Computer))
			}
			e.LogName = logName
			entries = append(entries, e)
		}

		startIdx = ei
	}

	return entries
}

func buildSyslogFromEvent(e wevtEntry) string {
	ts := time.Now().Format("Jan 2 15:04:05")
	if !e.Time.IsZero() {
		ts = e.Time.Format("Jan 2 15:04:05")
	}
	// host := e.Computer
	// if host == "" {
	// 	host = localHost
	// }
	host := localHost

	proc := e.Provider
	if proc == "" {
		proc = "eventlog"
	}
	// ensure single-line values
	proc = singleLine(sanitizeForSyslog(proc))
	//logname := singleLine(sanitizeForSyslog(e.LogName))
	pri := 134
	msg := strings.Builder{}
	msg.WriteString("<")
	msg.WriteString(intToString(pri))
	msg.WriteString(">")
	msg.WriteString(ts)
	msg.WriteString(" ")
	msg.WriteString(host)
	msg.WriteString(" ")
	// if logname != "" {
	// 	msg.WriteString("log=")
	// 	msg.WriteString(logname)
	// 	msg.WriteString(" ")
	// }
	msg.WriteString(proc)
	msg.WriteString("[info] ")
	// message should be single-line
	msg.WriteString(singleLine(sanitizeForSyslog(e.Message)))
	return msg.String()
}

// processed persistence helpers
func processedCachePath() string {
	// allow override via registry key
	if p, _ := ReadRegistryString("ProcessedCachePath"); p != "" {
		return p
	}
	// default: next to executable
	if ex, err := os.Executable(); err == nil {
		dir := filepath.Dir(ex)
		return filepath.Join(dir, "processed_cache.json")
	}
	// fallback to cwd
	return "processed_cache.json"
}

func loadProcessed() error {
	path := processedCachePath()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var m map[string]int64
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	processedMu.Lock()
	defer processedMu.Unlock()
	for k, v := range m {
		processed[k] = time.Unix(v, 0)
	}
	return nil
}

func saveProcessed() error {
	path := processedCachePath()
	// ensure directory exists
	dir := filepath.Dir(path)
	if dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	processedMu.Lock()
	copyMap := make(map[string]int64, len(processed))
	for k, t := range processed {
		copyMap[k] = t.Unix()
	}
	processedMu.Unlock()

	b, err := json.Marshal(copyMap)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// cleanupProcessed removes old processed entries beyond retention
func cleanupProcessed() {
	processedMu.Lock()
	defer processedMu.Unlock()
	now := time.Now()
	for k, t := range processed {
		if now.Sub(t) > processedRetention {
			delete(processed, k)
		}
	}
}
