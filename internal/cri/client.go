package cri

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
	defaultTimeout = 10 * time.Second
	pullTimeout    = 30 * time.Second
)

// Client represents a gRPC client to a CRI runtime.
type Client struct {
	conn          *grpc.ClientConn
	runtimeClient runtimeapi.RuntimeServiceClient
	imageClient   runtimeapi.ImageServiceClient
	socketPath    string
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

func (c *Client) checkConnected() error {
	if c.runtimeClient == nil {
		return fmt.Errorf("CRI runtime client not initialized, call Connect() first")
	}
	return nil
}

// ListPodSandbox lists existing pod sandboxes, optionally filtered by labels.
func (c *Client) ListPodSandbox(ctx context.Context, filter *runtimeapi.PodSandboxFilter) ([]*runtimeapi.PodSandbox, error) {
	if err := c.checkConnected(); err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	resp, err := c.runtimeClient.ListPodSandbox(callCtx, &runtimeapi.ListPodSandboxRequest{Filter: filter})
	if err != nil {
		return nil, fmt.Errorf("failed to list pod sandboxes: %w", err)
	}
	return resp.GetItems(), nil
}

// RunPodSandbox creates and starts a pod sandbox.
func (c *Client) RunPodSandbox(ctx context.Context, config *runtimeapi.PodSandboxConfig) (string, error) {
	if err := c.checkConnected(); err != nil {
		return "", err
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	resp, err := c.runtimeClient.RunPodSandbox(callCtx, &runtimeapi.RunPodSandboxRequest{Config: config})
	if err != nil {
		return "", fmt.Errorf("failed to run pod sandbox: %w", err)
	}
	return resp.GetPodSandboxId(), nil
}

// StopPodSandbox stops a running pod sandbox.
func (c *Client) StopPodSandbox(ctx context.Context, sandboxID string) error {
	if err := c.checkConnected(); err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	_, err := c.runtimeClient.StopPodSandbox(callCtx, &runtimeapi.StopPodSandboxRequest{PodSandboxId: sandboxID})
	if err != nil {
		return fmt.Errorf("failed to stop pod sandbox %s: %w", sandboxID, err)
	}
	return nil
}

// RemovePodSandbox removes a stopped pod sandbox.
func (c *Client) RemovePodSandbox(ctx context.Context, sandboxID string) error {
	if err := c.checkConnected(); err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	_, err := c.runtimeClient.RemovePodSandbox(callCtx, &runtimeapi.RemovePodSandboxRequest{PodSandboxId: sandboxID})
	if err != nil {
		return fmt.Errorf("failed to remove pod sandbox %s: %w", sandboxID, err)
	}
	return nil
}

// PodSandboxStatus returns the status of a pod sandbox, including its network IP.
func (c *Client) PodSandboxStatus(ctx context.Context, sandboxID string) (*runtimeapi.PodSandboxStatus, error) {
	if err := c.checkConnected(); err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	resp, err := c.runtimeClient.PodSandboxStatus(callCtx, &runtimeapi.PodSandboxStatusRequest{PodSandboxId: sandboxID})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod sandbox status %s: %w", sandboxID, err)
	}
	return resp.GetStatus(), nil
}

// ListContainers lists containers matching the optional filter.
func (c *Client) ListContainers(ctx context.Context, filter *runtimeapi.ContainerFilter) ([]*runtimeapi.Container, error) {
	if err := c.checkConnected(); err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	resp, err := c.runtimeClient.ListContainers(callCtx, &runtimeapi.ListContainersRequest{Filter: filter})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}
	return resp.GetContainers(), nil
}

// CreateContainer creates a new container within a pod sandbox.
func (c *Client) CreateContainer(ctx context.Context, sandboxID string, config *runtimeapi.ContainerConfig, sbConfig *runtimeapi.PodSandboxConfig) (string, error) {
	if err := c.checkConnected(); err != nil {
		return "", err
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	resp, err := c.runtimeClient.CreateContainer(callCtx, &runtimeapi.CreateContainerRequest{
		PodSandboxId:  sandboxID,
		Config:        config,
		SandboxConfig: sbConfig,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create container %s: %w", config.GetMetadata().GetName(), err)
	}
	return resp.GetContainerId(), nil
}

// StartContainer starts a created container.
func (c *Client) StartContainer(ctx context.Context, containerID string) error {
	if err := c.checkConnected(); err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	_, err := c.runtimeClient.StartContainer(callCtx, &runtimeapi.StartContainerRequest{ContainerId: containerID})
	if err != nil {
		return fmt.Errorf("failed to start container %s: %w", containerID, err)
	}
	return nil
}

// StopContainer stops a running container with the given grace period (seconds).
func (c *Client) StopContainer(ctx context.Context, containerID string, timeoutSecs int64) error {
	if err := c.checkConnected(); err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	_, err := c.runtimeClient.StopContainer(callCtx, &runtimeapi.StopContainerRequest{
		ContainerId: containerID,
		Timeout:     timeoutSecs,
	})
	if err != nil {
		return fmt.Errorf("failed to stop container %s: %w", containerID, err)
	}
	return nil
}

// RemoveContainer removes a stopped container.
func (c *Client) RemoveContainer(ctx context.Context, containerID string) error {
	if err := c.checkConnected(); err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	_, err := c.runtimeClient.RemoveContainer(callCtx, &runtimeapi.RemoveContainerRequest{ContainerId: containerID})
	if err != nil {
		return fmt.Errorf("failed to remove container %s: %w", containerID, err)
	}
	return nil
}

// PullImage pulls the specified container image.
func (c *Client) PullImage(ctx context.Context, image string) error {
	if c.imageClient == nil {
		return fmt.Errorf("CRI image client not initialized, call Connect() first")
	}
	callCtx, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()

	_, err := c.imageClient.PullImage(callCtx, &runtimeapi.PullImageRequest{
		Image: &runtimeapi.ImageSpec{Image: image},
	})
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %w", image, err)
	}
	return nil
}

// ImageStatus returns metadata about a local image, or nil if not present.
func (c *Client) ImageStatus(ctx context.Context, image string) (*runtimeapi.Image, error) {
	if c.imageClient == nil {
		return nil, fmt.Errorf("CRI image client not initialized, call Connect() first")
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	resp, err := c.imageClient.ImageStatus(callCtx, &runtimeapi.ImageStatusRequest{
		Image: &runtimeapi.ImageSpec{Image: image},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get image status %s: %w", image, err)
	}
	return resp.GetImage(), nil
}

// Version returns the runtime version information.
func (c *Client) Version(ctx context.Context) (*runtimeapi.VersionResponse, error) {
	if err := c.checkConnected(); err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	resp, err := c.runtimeClient.Version(callCtx, &runtimeapi.VersionRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get CRI runtime version: %w", err)
	}
	return resp, nil
}
