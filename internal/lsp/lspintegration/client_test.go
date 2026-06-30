// Package lspintegration drives the real `gala lsp` binary over JSON-RPC, the
// same transport IntelliJ / VS Code use. Unlike the in-process handler tests,
// it constructs URIs exactly as a real editor does, so it catches regressions
// in URI handling, sibling discovery, project-root resolution and the embedded
// standard library — divergences the in-process harness cannot see.
package lspintegration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// lspClient is a tiny JSON-RPC 2.0 client speaking LSP over a subprocess's
// stdin/stdout. It is deliberately minimal: enough to initialize, open files
// and collect publishDiagnostics notifications.
type lspClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader

	mu    sync.Mutex
	diags map[string][]diagnostic // uri -> latest diagnostics
	nextID int

	done chan struct{}
}

type diagnostic struct {
	Range struct {
		Start struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"start"`
	} `json:"range"`
	Severity int    `json:"severity"`
	Message  string `json:"message"`
}

type publishParams struct {
	URI         string       `json:"uri"`
	Diagnostics []diagnostic `json:"diagnostics"`
}

func startLSP(galaBin string) (*lspClient, error) {
	cmd := exec.Command(galaBin, "lsp")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &lspClient{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
		diags:  make(map[string][]diagnostic),
		done:   make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *lspClient) readLoop() {
	defer close(c.done)
	for {
		msg, err := c.readMessage()
		if err != nil {
			return
		}
		var env struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(msg, &env) != nil {
			continue
		}
		if env.Method == "textDocument/publishDiagnostics" {
			var p publishParams
			if json.Unmarshal(env.Params, &p) == nil {
				c.mu.Lock()
				c.diags[p.URI] = p.Diagnostics
				c.mu.Unlock()
			}
		}
	}
}

func (c *lspClient) readMessage() ([]byte, error) {
	contentLen := 0
	for {
		line, err := c.stdout.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			n, _ := strconv.Atoi(strings.TrimSpace(line[len("content-length:"):]))
			contentLen = n
		}
	}
	body := make([]byte, contentLen)
	if _, err := io.ReadFull(c.stdout, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (c *lspClient) send(msg map[string]interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return err
}

func (c *lspClient) notify(method string, params interface{}) error {
	return c.send(map[string]interface{}{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *lspClient) request(method string, params interface{}) error {
	c.nextID++
	return c.send(map[string]interface{}{"jsonrpc": "2.0", "id": c.nextID, "method": method, "params": params})
}

func (c *lspClient) initialize(rootDir string) error {
	rootURI := pathToFileURI(rootDir)
	if err := c.request("initialize", map[string]interface{}{
		"processId":    os.Getpid(),
		"rootUri":      rootURI,
		"capabilities": map[string]interface{}{},
		"workspaceFolders": []map[string]string{
			{"uri": rootURI, "name": "fixture"},
		},
	}); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	return c.notify("initialized", map[string]interface{}{})
}

func (c *lspClient) openFile(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return c.notify("textDocument/didOpen", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        pathToFileURI(path),
			"languageId": "gala",
			"version":    1,
			"text":       string(src),
		},
	})
}

func (c *lspClient) diagnosticsFor(path string) []diagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.diags[pathToFileURI(path)]
}

func (c *lspClient) close() {
	_ = c.notify("exit", map[string]interface{}{})
	_ = c.stdin.Close()
	_ = c.cmd.Process.Kill()
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
	}
	_ = c.cmd.Wait()
}

// pathToFileURI builds a file:// URI the way real editors do: "file://" plus
// the slash-normalized absolute path with a single leading slash.
func pathToFileURI(p string) string {
	s := filepath.ToSlash(p)
	if !strings.HasPrefix(s, "/") {
		s = "/" + s // Windows drive paths: C:/... -> /C:/...
	}
	return "file://" + s
}
