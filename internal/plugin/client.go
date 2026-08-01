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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tfcace/hash/internal/version"
)

const (
	initializeTimeout     = 500 * time.Millisecond
	pluginWriterQueueSize = 64
)

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
	Error   *RPCError       `json:"error"`
}

// RPCError is a JSON-RPC error returned across the plugin boundary.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return e.Message
}

// HostServiceHandler handles a plugin-originated host.* request.
type HostServiceHandler func(context.Context, Manifest, string, json.RawMessage) (any, *RPCError)

// SessionContext is the immutable interactive context sent during initialize.
type SessionContext struct {
	CWD     string
	Dialect string
}

type pendingResponse struct {
	response rpcResponse
	err      error
}

type queuedWrite struct {
	ctx  context.Context
	data []byte
	done chan error
}

// ProcessClient owns one warm plugin process and multiplexes JSON-RPC calls.
type ProcessClient struct {
	manifest Manifest
	cmd      *exec.Cmd
	stdin    io.WriteCloser

	mu         sync.Mutex
	pending    map[int64]chan pendingResponse
	active     map[int64]context.Context
	hostActive map[int64]context.CancelFunc
	host       HostServiceHandler
	closed     bool
	err        error
	nextID     atomic.Int64
	done       chan struct{}
	writes     chan queuedWrite
}

// StartProcess launches manifest.Entrypoint and completes the protocol
// handshake. Plugin processes inherit the shell environment by design.
func StartProcess(ctx context.Context, manifest Manifest, settings map[string]any) (*ProcessClient, error) {
	return StartProcessWithHandler(ctx, manifest, settings, nil)
}

// StartProcessWithHandler launches a plugin with bidirectional host services.
func StartProcessWithHandler(ctx context.Context, manifest Manifest, settings map[string]any, host HostServiceHandler) (*ProcessClient, error) {
	cwd, _ := os.Getwd()
	return StartProcessWithSession(ctx, manifest, settings, host, SessionContext{CWD: cwd, Dialect: "bash"})
}

// StartProcessWithSession launches a plugin and sends the complete protocol-v1
// initialization context.
func StartProcessWithSession(ctx context.Context, manifest Manifest, settings map[string]any, host HostServiceHandler, session SessionContext) (*ProcessClient, error) {
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
	if startErr := cmd.Start(); startErr != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start plugin %q: %w", manifest.ID, startErr)
	}

	client := &ProcessClient{
		manifest:   manifest,
		cmd:        cmd,
		stdin:      stdin,
		pending:    make(map[int64]chan pendingResponse),
		active:     make(map[int64]context.Context),
		hostActive: make(map[int64]context.CancelFunc),
		host:       host,
		done:       make(chan struct{}),
		writes:     make(chan queuedWrite, pluginWriterQueueSize),
	}
	go client.writeLoop()
	go client.readLoop(stdout)
	go client.waitLoop()

	initializeCtx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()
	var result struct {
		ProtocolVersion int `json:"protocol_version"`
	}
	err = client.Call(initializeCtx, "initialize", map[string]any{
		"protocol_version": ProtocolVersion,
		"hash_version":     version.String(),
		"plugin": map[string]string{
			"id":      manifest.ID,
			"version": manifest.Version,
		},
		"hooks":    manifest.Hooks,
		"settings": settings,
		"cwd":      session.CWD,
		"dialect":  session.Dialect,
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
func (c *ProcessClient) Call(ctx context.Context, method string, params, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := c.nextID.Add(1)
	pending := make(chan pendingResponse, 1)
	if err := c.registerPending(id, pending); err != nil {
		return err
	}
	c.mu.Lock()
	c.active[id] = ctx
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.active, id)
		c.mu.Unlock()
	}()
	if err := c.write(ctx, rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
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
		cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		_ = c.enqueueNotification(cancelCtx, rpcRequest{JSONRPC: "2.0", Method: "$/cancelRequest", Params: map[string]int64{"id": id}})
		cancel()
		return ctx.Err()
	case <-c.done:
		return c.failure()
	}
}

// Notify sends a JSON-RPC notification without waiting for a response.
func (c *ProcessClient) Notify(method string, params any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return c.write(ctx, rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
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

func (c *ProcessClient) write(ctx context.Context, request any) error {
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	job := queuedWrite{ctx: ctx, data: data, done: make(chan error, 1)}
	select {
	case c.writes <- job:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.failure()
	}
	select {
	case err := <-job.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.failure()
	}
}

func (c *ProcessClient) enqueueNotification(ctx context.Context, request any) error {
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	job := queuedWrite{ctx: context.Background(), data: data, done: make(chan error, 1)}
	select {
	case c.writes <- job:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.failure()
	}
}

func (c *ProcessClient) writeLoop() {
	for {
		select {
		case job := <-c.writes:
			if err := job.ctx.Err(); err != nil {
				job.done <- err
				continue
			}
			if _, err := c.stdin.Write(job.data); err != nil {
				wrapped := fmt.Errorf("write plugin %q: %w", c.manifest.ID, err)
				job.done <- wrapped
				c.abort(wrapped)
				return
			}
			job.done <- nil
		case <-c.done:
			return
		}
	}
}

func (c *ProcessClient) readLoop(stdout io.Reader) { //nolint:gocyclo // JSON-RPC frame classification is an explicit protocol state machine
	scanner := bufio.NewScanner(stdout)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1<<20)
	for scanner.Scan() {
		var message struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      *int64          `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
			Result  json.RawMessage `json:"result"`
			Error   *RPCError       `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			c.abort(fmt.Errorf("plugin %q emitted invalid JSON-RPC: %w", c.manifest.ID, err))
			return
		}
		if message.JSONRPC != "2.0" {
			c.abort(fmt.Errorf("plugin %q emitted an unsupported JSON-RPC message", c.manifest.ID))
			return
		}
		if message.Method == "$/cancelRequest" && message.ID == nil {
			var cancelParams struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal(message.Params, &cancelParams); err != nil || cancelParams.ID <= 0 {
				c.abort(fmt.Errorf("plugin %q emitted invalid cancellation", c.manifest.ID))
				return
			}
			c.mu.Lock()
			cancel := c.hostActive[cancelParams.ID]
			c.mu.Unlock()
			if cancel != nil {
				cancel()
			}
			continue
		}
		if message.ID == nil || *message.ID <= 0 {
			c.abort(fmt.Errorf("plugin %q emitted an unsupported JSON-RPC message", c.manifest.ID))
			return
		}
		if message.Method != "" {
			go c.handleHostRequest(*message.ID, message.Method, message.Params)
			continue
		}
		if (message.Error != nil && len(message.Result) != 0) || (message.Error == nil && len(message.Result) == 0) {
			c.abort(fmt.Errorf("plugin %q emitted an invalid JSON-RPC response", c.manifest.ID))
			return
		}
		response := rpcResponse{JSONRPC: message.JSONRPC, ID: *message.ID, Result: message.Result, Error: message.Error}
		c.mu.Lock()
		pending := c.pending[response.ID]
		delete(c.pending, response.ID)
		c.mu.Unlock()
		if pending != nil {
			pending <- pendingResponse{response: response}
		}
	}
	if err := scanner.Err(); err != nil {
		c.abort(fmt.Errorf("read plugin %q: %w", c.manifest.ID, err))
		return
	}
	c.abort(fmt.Errorf("plugin %q closed protocol stdout", c.manifest.ID))
}

func (c *ProcessClient) abort(err error) {
	c.fail(err)
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}

// Exited reports that the peer has closed or been terminated.
func (c *ProcessClient) Exited() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *ProcessClient) handleHostRequest(id int64, method string, params json.RawMessage) {
	responseCtx := context.Background()
	respond := func(result any, rpcErr *RPCError) {
		message := map[string]any{"jsonrpc": "2.0", "id": id}
		if rpcErr != nil {
			message["error"] = rpcErr
		} else {
			message["result"] = result
		}
		ctx, cancel := context.WithTimeout(responseCtx, 500*time.Millisecond)
		defer cancel()
		_ = c.write(ctx, message)
	}
	if !strings.HasPrefix(method, "host.") || !declaresHostService(c.manifest, strings.TrimPrefix(method, "host.")) {
		respond(nil, &RPCError{Code: -32601, Message: "host service not declared"})
		return
	}
	parentID, err := parentRequestID(params)
	if err != nil {
		respond(nil, &RPCError{Code: -32602, Message: err.Error()})
		return
	}
	c.mu.Lock()
	parentCtx := c.active[parentID]
	c.mu.Unlock()
	if parentCtx == nil || parentCtx.Err() != nil {
		respond(nil, &RPCError{Code: -32800, Message: "parent request is not active"})
		return
	}
	responseCtx = parentCtx
	if c.host == nil {
		respond(nil, &RPCError{Code: -32601, Message: "host services unavailable"})
		return
	}
	hostCtx, cancel := context.WithCancel(parentCtx)
	c.mu.Lock()
	c.hostActive[id] = cancel
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		delete(c.hostActive, id)
		c.mu.Unlock()
	}()
	responseCtx = hostCtx
	result, rpcErr := c.host(hostCtx, c.manifest, method, params)
	respond(result, rpcErr)
}

func declaresHostService(manifest Manifest, service string) bool {
	for _, declared := range manifest.Capabilities.HostServices {
		if declared == service || declared == "host."+service {
			return true
		}
	}
	return false
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
	shutdownErr := c.Call(ctx, "shutdown", map[string]any{}, &ignored)
	_ = c.stdin.Close()
	select {
	case <-c.done:
		return shutdownErr
	case <-ctx.Done():
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		if shutdownErr != nil {
			return fmt.Errorf("plugin shutdown: %w", shutdownErr)
		}
		return fmt.Errorf("plugin shutdown timed out")
	}
}
