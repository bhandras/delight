package sdk

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	logTailLines    = 100
	logFileMaxBytes = 2 * 1024 * 1024
	logFileMaxFiles = 5
)

type logManager struct {
	mu       sync.Mutex
	dir      string
	baseName string
	file     *os.File
	size     int64
	tail     []string
}

func newLogManager() *logManager {
	return &logManager{
		baseName: "sdk.log",
	}
}

func (m *logManager) setDir(dir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if dir == "" {
		return fmt.Errorf("log directory is empty")
	}
	if m.dir == dir && m.file != nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	m.dir = dir
	return m.openLocked()
}

func (m *logManager) appendLogLine(line string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appendTailLocked(line)
	m.appendFileLocked(line)
}

func (m *logManager) appendPanic(line string) {
	m.appendLogLine(line)
}

func (m *logManager) appendTailLocked(line string) {
	segments := strings.Split(line, "\n")
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		m.tail = append(m.tail, segment)
		if len(m.tail) > logTailLines {
			m.tail = m.tail[len(m.tail)-logTailLines:]
		}
	}
}

func (m *logManager) appendFileLocked(line string) {
	if m.dir == "" {
		if err := m.setDir(defaultLogDir()); err != nil {
			return
		}
	}
	if m.file == nil {
		if err := m.openLocked(); err != nil {
			return
		}
	}
	n, err := m.file.WriteString(line)
	if err != nil {
		return
	}
	m.size += int64(n)
	if m.size >= logFileMaxBytes {
		_ = m.rotateLocked()
	}
}

func (m *logManager) openLocked() error {
	if m.dir == "" {
		return fmt.Errorf("log directory not set")
	}
	path := filepath.Join(m.dir, m.baseName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err == nil {
		m.size = info.Size()
	}
	m.file = file
	return nil
}

func (m *logManager) rotateLocked() error {
	if m.file != nil {
		_ = m.file.Close()
		m.file = nil
	}
	base := filepath.Join(m.dir, m.baseName)
	for i := logFileMaxFiles - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", base, i)
		dst := fmt.Sprintf("%s.%d", base, i+1)
		if i == logFileMaxFiles-1 {
			_ = os.Remove(dst)
		}
		_ = os.Rename(src, dst)
	}
	_ = os.Rename(base, fmt.Sprintf("%s.1", base))
	m.size = 0
	return m.openLocked()
}

func (m *logManager) getLogsSnapshot() []byte {
	m.mu.Lock()
	dir := m.dir
	base := m.baseName
	m.mu.Unlock()
	if dir == "" {
		return nil
	}
	var out []byte
	basePath := filepath.Join(dir, base)
	for i := logFileMaxFiles - 1; i >= 1; i-- {
		path := fmt.Sprintf("%s.%d", basePath, i)
		if data, err := os.ReadFile(path); err == nil {
			out = append(out, data...)
		}
	}
	if data, err := os.ReadFile(basePath); err == nil {
		out = append(out, data...)
	}
	return out
}

var sdkLogs = newLogManager()
var stderrCaptureOnce sync.Once
var stderrCaptureEnabled bool
var stderrRedirected bool

func init() {
	debug.SetTraceback("all")
	debug.SetPanicOnFault(true)
	if runtime.GOOS == "ios" {
		redirectStderrToLogFile()
	} else {
		startStderrCapture()
	}
}

func logLine(line string) {
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return
	}
	raw := line + "\n"
	if stderrRedirected {
		sdkLogs.appendLogLine(formatLine(raw))
		return
	}
	fmt.Fprint(os.Stderr, raw)
	if !stderrCaptureEnabled {
		sdkLogs.appendLogLine(formatLine(raw))
	}
}

func logPanic(context string, value interface{}) {
	stack := string(debug.Stack())
	line := fmt.Sprintf("GO PANIC: %s: %v\n%s\n", context, value, stack)
	if stderrRedirected {
		sdkLogs.appendPanic(line)
		return
	}
	fmt.Fprint(os.Stderr, line)
	sdkLogs.appendPanic(line)
}

func startStderrCapture() {
	stderrCaptureOnce.Do(func() {
		orig := os.Stderr
		reader, writer, err := os.Pipe()
		if err != nil {
			return
		}
		os.Stderr = writer
		stderrCaptureEnabled = true
		go func() {
			defer reader.Close()
			r := bufio.NewReader(reader)
			for {
				line, err := r.ReadString('\n')
				if line != "" {
					sdkLogs.appendLogLine(formatLine(line))
					_, _ = orig.Write([]byte(line))
				}
				if err != nil {
					return
				}
			}
		}()
	})
}

func redirectStderrToLogFile() {
	stderrCaptureOnce.Do(func() {
		if err := sdkLogs.setDir(defaultLogDir()); err != nil {
			return
		}
		sdkLogs.mu.Lock()
		dir := sdkLogs.dir
		base := sdkLogs.baseName
		sdkLogs.mu.Unlock()
		if dir == "" {
			return
		}
		path := filepath.Join(dir, base)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		if err := syscall.Dup2(int(file.Fd()), int(os.Stderr.Fd())); err != nil {
			_ = file.Close()
			return
		}
		os.Stderr = file
		stderrRedirected = true
	})
}

func formatLine(line string) string {
	trimmed := strings.TrimRight(line, "\n")
	if trimmed == "" {
		return ""
	}
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	return fmt.Sprintf("%s %s\n", ts, trimmed)
}

func defaultLogDir() string {
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return filepath.Join(dir, "delight")
	}
	return os.TempDir()
}

func localIP() string {
	iface, err := net.InterfaceByName("en0")
	if err == nil {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ip := addrToIP(addr)
			if ip != "" {
				return ip
			}
		}
	}
	return "127.0.0.1"
}

func addrToIP(addr net.Addr) string {
	switch v := addr.(type) {
	case *net.IPNet:
		if ip := v.IP.To4(); ip != nil {
			return ip.String()
		}
	case *net.IPAddr:
		if ip := v.IP.To4(); ip != nil {
			return ip.String()
		}
	}
	return ""
}

func (c *Client) StartLogServer() (string, error) {
	c.mu.Lock()
	if c.logServer != nil {
		url := c.logServerURL
		c.mu.Unlock()
		return url, nil
	}
	c.mu.Unlock()

	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return "", err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head><meta charset="utf-8"><title>Delight SDK Logs</title></head>
  <body>
    <h1>Delight SDK Logs</h1>
    <ul>
      <li><a href="/sdk.log">sdk.log</a></li>
    </ul>
  </body>
</html>`))
	})
	mux.HandleFunc("/sdk.log", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(sdkLogs.getLogsSnapshot())
	})
	server := &http.Server{Handler: mux}
	go func() {
		_ = server.Serve(listener)
	}()

	host := localIP()
	port := listener.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://%s:%d", host, port)

	c.mu.Lock()
	c.logServer = server
	c.logServerURL = url
	c.mu.Unlock()
	logLine("Go log server started at " + url)
	return url, nil
}

func (c *Client) StopLogServer() error {
	c.mu.Lock()
	server := c.logServer
	c.logServer = nil
	c.logServerURL = ""
	c.mu.Unlock()
	if server == nil {
		return nil
	}
	logLine("Go log server stopped")
	return server.Close()
}

func sanitizeContext(context string) string {
	return strings.TrimSpace(context)
}
