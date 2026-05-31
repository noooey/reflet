package runner

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type EnvPair struct {
	Name  string
	Value string
}

type EnvPairs []EnvPair

func (p *EnvPairs) String() string {
	out := make([]string, 0, len(*p))
	for _, pair := range *p {
		out = append(out, pair.Name+"="+pair.Value)
	}
	return strings.Join(out, ",")
}

func (p *EnvPairs) Set(value string) error {
	name, secretRef, ok := strings.Cut(value, "=")
	if !ok {
		return fmt.Errorf("invalid env assignment %q", value)
	}
	if name == "" || secretRef == "" {
		return fmt.Errorf("invalid env assignment %q", value)
	}
	if !strings.HasPrefix(secretRef, "ref://") {
		return fmt.Errorf("environment value must be a ref:// string: %q", value)
	}
	*p = append(*p, EnvPair{Name: name, Value: secretRef})
	return nil
}

type Runner struct {
	addr   string
	caPath string
}

func New(addr, caPath string) *Runner {
	return &Runner{addr: addr, caPath: caPath}
}

func (r *Runner) Run(cmdArgs []string, envPairs []EnvPair) error {
	if err := r.ensureProxy(); err != nil {
		return err
	}

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = buildEnv(os.Environ(), envPairs, r.addr, r.caPath)

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				os.Exit(status.ExitStatus())
			}
			os.Exit(1)
		}
		return err
	}
	return nil
}

func (r *Runner) ensureProxy() error {
	if proxyHealthy(r.addr) {
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	logPath := filepath.Join(filepath.Dir(r.caPath), "..", "logs", "proxy.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Clean(logPath), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open proxy log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "proxy", "-addr", r.addr)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start proxy: %w", err)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if proxyHealthy(r.addr) {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("proxy did not become healthy at %s", r.addr)
}

func proxyHealthy(addr string) bool {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get("http://" + addr + "/__reflet/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func buildEnv(base []string, pairs []EnvPair, addr, caPath string) []string {
	values := make(map[string]string, len(base)+len(pairs)+8)
	for _, item := range base {
		name, value, ok := strings.Cut(item, "=")
		if ok {
			values[name] = value
		}
	}

	for _, pair := range pairs {
		values[pair.Name] = pair.Value
	}

	proxyURL := "http://" + addr
	values["HTTP_PROXY"] = proxyURL
	values["HTTPS_PROXY"] = proxyURL
	values["http_proxy"] = proxyURL
	values["https_proxy"] = proxyURL
	values["REFLET_ENABLED"] = "true"
	values["SSL_CERT_FILE"] = caPath
	values["REQUESTS_CA_BUNDLE"] = caPath
	values["CURL_CA_BUNDLE"] = caPath
	values["NODE_EXTRA_CA_CERTS"] = caPath
	values["GIT_SSL_CAINFO"] = caPath

	out := make([]string, 0, len(values))
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	return out
}
