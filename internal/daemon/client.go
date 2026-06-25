package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// SocketName returns the Unix socket path for the given device serial.
// Each device gets its own daemon socket, isolating sessions per device.
func SocketName(serial string) string {
	return fmt.Sprintf("/tmp/phonefast-%d-%s.sock", os.Getuid(), serial)
}

// PidFileName returns the PID file path for the given device serial.
func PidFileName(serial string) string {
	return fmt.Sprintf("/tmp/phonefast-%d-%s.pid", os.Getuid(), serial)
}

// DefaultSocketName returns the legacy UID-only socket path.
// Used only during migration to clean up old daemon instances.
func DefaultSocketName() string {
	return fmt.Sprintf("/tmp/phonefast-%d.sock", os.Getuid())
}

// DefaultPidFileName returns the legacy UID-only PID file path.
func DefaultPidFileName() string {
	return fmt.Sprintf("/tmp/phonefast-%d.pid", os.Getuid())
}

// Client represents a short-lived CLI client connected to a device-specific daemon.
type Client struct {
	socketPath string
	timeout    time.Duration
}

// NewClient creates a client for the daemon bound to the given device serial.
func NewClient(serial string) *Client {
	return &Client{
		socketPath: SocketName(serial),
		timeout:    30 * time.Second,
	}
}

// NewClientWithPath creates a client for a daemon at a specific socket path.
func NewClientWithPath(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
		timeout:    30 * time.Second,
	}
}

// Call sends a JSON-RPC request to the daemon and returns the result.
func (c *Client) Call(method string, params map[string]any) (json.RawMessage, error) {
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("daemon not running (is '%s daemon' started?): %w", filepath.Base(os.Args[0]), err)
	}
	defer conn.Close()

	req := Request{
		JSONRPC: "2.0",
		Method:  method,
		ID:      1,
	}
	if params != nil {
		data, _ := json.Marshal(params)
		req.Params = data
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	reqBytes = append(reqBytes, '\n')

	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(reqBytes); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	conn.SetReadDeadline(time.Now().Add(c.timeout))
	reader := bufio.NewReader(conn)
	respBytes, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Message)
	}

	return resp.Result, nil
}

// Ping checks if the daemon is reachable and returns its status.
func (c *Client) Ping() (map[string]any, error) {
	result, err := c.Call("status", nil)
	if err != nil {
		return nil, err
	}
	var status map[string]any
	if err := json.Unmarshal(result, &status); err != nil {
		return nil, fmt.Errorf("unmarshal status: %w", err)
	}
	return status, nil
}
