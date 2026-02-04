package runtime

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const (
	// defaultTimeout is the default timeout for CRI requests.
	defaultTimeout = 15 * time.Second
)

// Client represents a gRPC client to a CRI runtime.
type Client struct {
	conn         *grpc.ClientConn
	runtimeClient runtimeapi.RuntimeServiceClient
	imageClient   runtimeapi.ImageServiceClient
	socketPath   string
}

// NewClient creates a new CRI client.
func NewClient(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
	}
}

// Connect establishes a connection to the CRI runtime via the specified socket.
func (c *Client) Connect(ctx context.Context) error {
	log.Printf("Connecting to CRI socket: %s", c.socketPath)

	dialCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	// Strip the unix:// prefix if present, as net.Dial expects a filesystem path for unix sockets.
	addr := c.socketPath
	if strings.HasPrefix(addr, "unix://") {
		addr = strings.TrimPrefix(addr, "unix://")
	}

	conn, err := grpc.DialContext(dialCtx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, target string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", target)
		}),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to CRI socket %s: %w", c.socketPath, err)
	}

	c.conn = conn
	c.runtimeClient = runtimeapi.NewRuntimeServiceClient(conn)
	c.imageClient = runtimeapi.NewImageServiceClient(conn)

	log.Printf("Successfully connected to CRI socket: %s", c.socketPath)
	return nil
}

// Close closes the connection to the CRI runtime.
func (c *Client) Close() error {
	if c.conn != nil {
		log.Printf("Closing CRI connection to %s", c.socketPath)
		return c.conn.Close()
	}
	return nil
}

// ListPodSandbox lists existing pod sandboxes.
func (c *Client) ListPodSandbox(ctx context.Context) ([]*runtimeapi.PodSandbox, error) {
	if c.runtimeClient == nil {
		return nil, fmt.Errorf("CRI runtime client not initialized, call Connect() first")
	}

	callCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	resp, err := c.runtimeClient.ListPodSandbox(callCtx, &runtimeapi.ListPodSandboxRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list pod sandboxes: %w", err)
	}

	return resp.GetItems(), nil
}

// Version returns the runtime version information.
func (c *Client) Version(ctx context.Context) (*runtimeapi.VersionResponse, error) {
	if c.runtimeClient == nil {
		return nil, fmt.Errorf("CRI runtime client not initialized, call Connect() first")
	}

	callCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	resp, err := c.runtimeClient.Version(callCtx, &runtimeapi.VersionRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get CRI runtime version: %w", err)
	}
	return resp, nil
}
