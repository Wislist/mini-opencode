package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

type Client interface {
	Initialize(ctx context.Context) error
	ListTools(ctx context.Context) ([]ToolDef, error)
	CallTool(ctx context.Context, name string, args json.RawMessage) (CallToolResult, error)
	Close() error
}

type StdioClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.Reader
	nextID  int64
	writeMu sync.Mutex
	waitMu  sync.Mutex
	pending map[int64]chan jsonRPCResponse
}

func NewStdioClient(command string, args ...string) (*StdioClient, error) {
	cmd := exec.Command(command, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp start: %w", err)
	}

	client := &StdioClient{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		pending: map[int64]chan jsonRPCResponse{},
	}
	go client.readLoop()
	return client, nil
}

func (c *StdioClient) Initialize(ctx context.Context) error {
	_, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "mini-opencode",
			"version": "0.2.0",
		},
	})
	return err
}

func (c *StdioClient) ListTools(ctx context.Context) ([]ToolDef, error) {
	raw, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}

	var result toolListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp parse tools/list: %w", err)
	}
	return result.Tools, nil
}

func (c *StdioClient) CallTool(ctx context.Context, name string, args json.RawMessage) (CallToolResult, error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	raw, err := c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return CallToolResult{}, err
	}

	var result CallToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return CallToolResult{}, fmt.Errorf("mcp parse tools/call: %w", err)
	}
	return result, nil
}

func (c *StdioClient) Close() error {
	_ = c.stdin.Close()
	return c.cmd.Wait()
}

func (c *StdioClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	ch := make(chan jsonRPCResponse, 1)

	c.waitMu.Lock()
	c.pending[id] = ch
	c.waitMu.Unlock()

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		c.forget(id)
		return nil, err
	}
	data = append(data, '\n')

	c.writeMu.Lock()
	_, err = c.stdin.Write(data)
	c.writeMu.Unlock()
	if err != nil {
		c.forget(id)
		return nil, fmt.Errorf("mcp write: %w", err)
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.forget(id)
		return nil, ctx.Err()
	}
}

func (c *StdioClient) forget(id int64) {
	c.waitMu.Lock()
	delete(c.pending, id)
	c.waitMu.Unlock()
}

func (c *StdioClient) readLoop() {
	dec := json.NewDecoder(bufio.NewReader(c.stdout))
	for {
		var resp jsonRPCResponse
		if err := dec.Decode(&resp); err != nil {
			return
		}

		c.waitMu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.waitMu.Unlock()

		if ok {
			ch <- resp
		}
	}
}
