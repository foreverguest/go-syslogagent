package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"time"

	"strconv"

	"github.com/kardianos/service"
)

var (
	svcConfig = &service.Config{
		Name:        "SyslogAgentGo",
		DisplayName: "Syslog Agent (Go)",
		Description: "Forwards logs to a syslog server",
	}
)

type program struct {
	server string
	file   string
	exit   chan struct{}
}

func (p *program) Start(s service.Service) error {
	p.exit = make(chan struct{})
	go p.run()
	return nil
}

func (p *program) run() {
	// If file provided, tail that file; otherwise do nothing in service mode
	// We will read registry for server if empty
	if p.server == "" {
		if v, err := ReadRegistryString("SyslogServer"); err == nil && v != "" {
			p.server = v
		}
	}

	// start event log poller (Application, System, Security) with default interval
	interval := 10
	if v, err := ReadRegistryString("EventLogPollInterval"); err == nil && v != "" {
		if iv, err2 := strconv.Atoi(v); err2 == nil && iv > 0 {
			interval = iv
		}
	}
	logs := []string{"Application", "System", "Security"}
	StartEventLogPoller(logs, interval, p.server)

	// If file provided, also read it
	if p.file != "" {
		var scanner *bufio.Scanner
		f, err := os.Open(p.file)
		if err != nil {
			return
		}
		defer f.Close()
		scanner = bufio.NewScanner(f)

		for scanner.Scan() {
			select {
			case <-p.exit:
				return
			default:
			}
			line := scanner.Text()
			msg := ParseMessage(line)
			if msg == "" {
				continue
			}
			if debugMode {
				Debug("Parsed input message: %s", msg)
			}
			_ = SendSyslog(msg, p.server)
			time.Sleep(1 * time.Millisecond)
		}
	} else {
		// service mode: wait until stop
		<-p.exit
		return
	}
}

func (p *program) Stop(s service.Service) error {
	close(p.exit)
	return nil
}

func main() {
	// initialize local logger as early as possible
	InitLogger()

	install := flag.Bool("install", false, "install as service")
	remove := flag.Bool("remove", false, "remove service")
	server := flag.String("server", "", "syslog server address host:port")
	file := flag.String("file", "", "input file to read lines from (default stdin)")
	poll := flag.Int("poll", 0, "event log poll interval in seconds (saved on install)")
	console := flag.Bool("console", false, "run in console mode (start poller and read stdin)")
	debug := flag.Bool("debug", false, "print debug events found and sent")
	flag.Parse()

	prg := &program{server: *server, file: *file}
	debugMode = *debug
	s, err := service.New(prg, svcConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "service new error: %v\n", err)
		os.Exit(1)
	}

	if *install {
		// If a server address was provided, save it to the registry before installing
		if *server != "" {
			if err := WriteRegistryString("SyslogServer", *server); err != nil {
				LogWarn("failed to write SyslogServer to registry: %v", err)
			}
		}
		// If a poll interval was provided (>0), validate and save it as EventLogPollInterval
		if *poll > 0 {
			minPoll := 5
			maxPoll := 3600
			val := *poll
			if val < minPoll {
				LogWarn("poll interval %d too small, using %d", val, minPoll)
				val = minPoll
			}
			if val > maxPoll {
				LogWarn("poll interval %d too large, using %d", val, maxPoll)
				val = maxPoll
			}
			if err := WriteRegistryString("EventLogPollInterval", strconv.Itoa(val)); err != nil {
				LogWarn("failed to write EventLogPollInterval to registry: %v", err)
			}
		}

		err = s.Install()
		if err != nil {
			LogError("service install error: %v", err)
			fmt.Fprintf(os.Stderr, "service install error: %v\n", err)
		} else {
			LogInfo("service installed")
			fmt.Println("service installed")
		}
		return
	}

	if *remove {
		err = s.Uninstall()
		if err != nil {
			LogError("service remove error: %v", err)
			fmt.Fprintf(os.Stderr, "service remove error: %v\n", err)
		} else {
			LogInfo("service removed")
			fmt.Println("service removed")
		}
		return
	}

	// If -console flag provided, run in console mode (start poller and/or read stdin/file)
	if *console {
		runConsole(*server, *file, *poll)
		return
	}

	// If running interactively and not a service, run as console
	if service.Interactive() {
		// run as console app
		runConsole(*server, *file, *poll)
		return
	}

	err = s.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "service run error: %v\n", err)
	}
}

func runConsole(server, file string, poll int) {
	var scanner *bufio.Scanner
	if file != "" {
		f, err := os.Open(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to open file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		scanner = bufio.NewScanner(f)
	} else {
		scanner = bufio.NewScanner(os.Stdin)
	}

	if server == "" {
		if v, err := ReadRegistryString("SyslogServer"); err == nil && v != "" {
			server = v
		} else {
			server = "127.0.0.1:514"
		}
	}

	// start event poller if requested (poll>0) or if registry has value and poll==0
	interval := poll
	if interval <= 0 {
		if v, err := ReadRegistryString("EventLogPollInterval"); err == nil && v != "" {
			if iv, err2 := strconv.Atoi(v); err2 == nil && iv > 0 {
				interval = iv
			}
		}
	}
	if interval > 0 {
		logs := []string{"Application", "System", "Security"}
		StartEventLogPoller(logs, interval, server)
	}

	for scanner.Scan() {
		line := scanner.Text()
		msg := ParseMessage(line)
		if msg == "" {
			continue
		}
		if debugMode {
			Debug("Parsed input message: %s", msg)
		}
		if err := SendSyslog(msg, server); err != nil {
			fmt.Fprintf(os.Stderr, "send error: %v\n", err)
		}
	}
}
