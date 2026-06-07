package completion

import (
	"context"
	"path/filepath"
	"sync"
)

type contextLinesReader struct {
	mu       sync.Mutex
	inflight map[string]*contextLinesReadCall
}

type contextLinesReadCall struct {
	done  chan struct{}
	lines []string
	err   error
}

func (r *contextLinesReader) read(
	ctx context.Context,
	readFile func(string) ([]string, error),
	path string,
) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := contextReadKey(path)

	r.mu.Lock()
	if call, ok := r.inflight[key]; ok {
		r.mu.Unlock()
		return waitForLinesRead(ctx, call)
	}

	call := &contextLinesReadCall{done: make(chan struct{})}
	if r.inflight == nil {
		r.inflight = make(map[string]*contextLinesReadCall)
	}
	r.inflight[key] = call
	r.mu.Unlock()

	go r.finishLinesRead(path, key, readFile, call)
	return waitForLinesRead(ctx, call)
}

func (r *contextLinesReader) finishLinesRead(
	path string,
	key string,
	readFile func(string) ([]string, error),
	call *contextLinesReadCall,
) {
	lines, err := readFile(path)

	r.mu.Lock()
	call.lines = lines
	call.err = err
	delete(r.inflight, key)
	r.mu.Unlock()
	close(call.done)
}

func waitForLinesRead(ctx context.Context, call *contextLinesReadCall) ([]string, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-call.done:
		return append([]string(nil), call.lines...), call.err
	}
}

type contextBytesReader struct {
	mu       sync.Mutex
	inflight map[string]*contextBytesReadCall
}

type contextBytesReadCall struct {
	done chan struct{}
	data []byte
	err  error
}

func (r *contextBytesReader) read(
	ctx context.Context,
	readFile func(string) ([]byte, error),
	path string,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := contextReadKey(path)

	r.mu.Lock()
	if call, ok := r.inflight[key]; ok {
		r.mu.Unlock()
		return waitForBytesRead(ctx, call)
	}

	call := &contextBytesReadCall{done: make(chan struct{})}
	if r.inflight == nil {
		r.inflight = make(map[string]*contextBytesReadCall)
	}
	r.inflight[key] = call
	r.mu.Unlock()

	go r.finishBytesRead(path, key, readFile, call)
	return waitForBytesRead(ctx, call)
}

func (r *contextBytesReader) finishBytesRead(
	path string,
	key string,
	readFile func(string) ([]byte, error),
	call *contextBytesReadCall,
) {
	data, err := readFile(path)

	r.mu.Lock()
	call.data = data
	call.err = err
	delete(r.inflight, key)
	r.mu.Unlock()
	close(call.done)
}

func waitForBytesRead(ctx context.Context, call *contextBytesReadCall) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-call.done:
		return append([]byte(nil), call.data...), call.err
	}
}

func contextReadKey(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
