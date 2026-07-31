package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

const initializeTimeout = 500 * time.Millisecond

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return e.Message
}

type pendingResponse struct {
	response rpcResponse
	err      error
}

// ProcessClient owns one warm plugin process and multiplexes JSON-RPC calls.
type ProcessClient struct {
	manifest Manifest
	cmd      *exec.Cmd
	stdin    io.WriteCloser

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[int64]chan pendingResponse
	closed  bool
	err     error
	nextID  atomic.Int64
	done    chan struct{}
}

// StartProcess launches manifest.Entrypoint and completes the protocol
// handshake. Plugin processes inherit the shell environment by design.
func StartProcess(ctx context.Context, manifest Manifest, settings map[string]any) (*ProcessClient, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	cmd := exec.Command(manifest.Executable()) //nolint:gosec // manifest is explicitly enabled by the user
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("plugin stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("plugin stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start plugin %q: %w", manifest.ID, err)
	}

	client := &ProcessClient{
		manifest: manifest,
		cmd:      cmd,
		stdin:    stdin,
		pending:  make(map[int64]chan pendingResponse),
		done:     make(chan struct{}),
	}
	go client.readLoop(stdout)
	go client.waitLoop()

	initializeCtx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()
	var result struct {
		ProtocolVersion int `json:"protocol_version"`
	}
	err = client.Call(initializeCtx, "initialize", map[string]any{
		"protocol_version": ProtocolVersion,
		"plugin": map[string]string{
			"id":      manifest.ID,
			"version": manifest.Version,
		},
		"settings": settings,
	}, &result)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("initialize plugin %q: %w", manifest.ID, err)
	}
	if result.ProtocolVersion != ProtocolVersion {
		_ = client.Close()
		return nil, fmt.Errorf("plugin %q returned unsupported protocol_version %d", manifest.ID, result.ProtocolVersion)
	}
	return client, nil
}

// Call invokes a plugin method and decodes its result into result.
func (c *ProcessClient) Call(ctx context.Context, method string, params any, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := c.nextID.Add(1)
	pending := make(chan pendingResponse, 1)
	if err := c.registerPending(id, pending); err != nil {
		return err
	}
	if err := c.write(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		c.removePending(id)
		return err
	}

	select {
	case response := <-pending:
		if response.err != nil {
			return response.err
		}
		if response.response.Error != nil {
			return fmt.Errorf("plugin %q %s: %w", c.manifest.ID, method, response.response.Error)
		}
		if result == nil || len(response.response.Result) == 0 || string(response.response.Result) == "null" {
			return nil
		}
		if err := json.Unmarshal(response.response.Result, result); err != nil {
			return fmt.Errorf("decode plugin %q %s result: %w", c.manifest.ID, method, err)
		}
		return nil
	case <-ctx.Done():
		c.removePending(id)
		_ = c.Notify("$/cancelRequest", map[string]int64{"id": id})
		return ctx.Err()
	case <-c.done:
		return c.failure()
	}
}

// Notify sends a JSON-RPC notification without waiting for a response.
func (c *ProcessClient) Notify(method string, params any) error {
	return c.write(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *ProcessClient) registerPending(id int64, pending chan pendingResponse) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		if c.err != nil {
			return c.err
		}
		return errors.New("plugin client is closed")
	}
	c.pending[id] = pending
	return nil
}

func (c *ProcessClient) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *ProcessClient) write(request rpcRequest) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	closed := c.closed
	err := c.err
	c.mu.Unlock()
	if closed {
		if err != nil {
			return err
		}
		return errors.New("plugin client is closed")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := c.stdin.Write(data); err != nil {
		c.fail(fmt.Errorf("write plugin %q: %w", c.manifest.ID, err))
		return c.failure()
	}
	return nil
}

func (c *ProcessClient) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1<<20)
	for scanner.Scan() {
		var response rpcResponse
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			c.fail(fmt.Errorf("plugin %q emitted invalid JSON-RPC: %w", c.manifest.ID, err))
			return
		}
		if response.JSONRPC != "2.0" || response.ID <= 0 || (response.Error != nil && len(response.Result) != 0) || (response.Error == nil && len(response.Result) == 0) {
			c.fail(fmt.Errorf("plugin %q emitted an unsupported JSON-RPC message", c.manifest.ID))
			return
		}
		c.mu.Lock()
		pending := c.pending[response.ID]
		delete(c.pending, response.ID)
		c.mu.Unlock()
		if pending != nil {
			pending <- pendingResponse{response: response}
		}
	}
	if err := scanner.Err(); err != nil {
		c.fail(fmt.Errorf("read plugin %q: %w", c.manifest.ID, err))
	}
}

func (c *ProcessClient) waitLoop() {
	err := c.cmd.Wait()
	if err != nil {
		c.fail(fmt.Errorf("plugin %q exited: %w", c.manifest.ID, err))
		return
	}
	c.fail(fmt.Errorf("plugin %q exited", c.manifest.ID))
}

func (c *ProcessClient) fail(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.err = err
	pending := c.pending
	c.pending = make(map[int64]chan pendingResponse)
	close(c.done)
	c.mu.Unlock()
	for _, request := range pending {
		request <- pendingResponse{err: err}
	}
}

func (c *ProcessClient) failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	return errors.New("plugin client is closed")
}

// Close requests plugin shutdown and then terminates the process if necessary.
func (c *ProcessClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	var ignored map[string]any
	_ = c.Call(ctx, "shutdown", map[string]any{}, &ignored)
	_ = c.stdin.Close()
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		return nil
	}
}
