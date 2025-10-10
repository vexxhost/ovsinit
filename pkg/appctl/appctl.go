package appctl

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/cenkalti/rpc2"
	"github.com/cenkalti/rpc2/jsonrpc"
)

const (
	RUN_DIR = "/run/openvswitch"
)

var ErrNoPidFile = errors.New("pid file does not exist")

type Client struct {
	*rpc2.Client
}

func NewClient(conn io.ReadWriteCloser) *Client {
	client := rpc2.NewClientWithCodec(jsonrpc.NewJSONCodec(conn))
	client.SetBlocking(true)
	go client.Run()

	return &Client{
		Client: client,
	}
}

func (c *Client) Close() error {
	return c.Client.Close()
}

func Dial(network, address string) (*Client, error) {
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}

	return NewClient(conn), nil
}

func DialBinary(binary string) (*Client, error) {
	path := fmt.Sprintf("%s/%s.pid", RUN_DIR, binary)
	bytes, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoPidFile
		}
		return nil, fmt.Errorf("failed to read pid file %s: %w", path, err)
	}

	var pid int
	_, err = fmt.Sscanf(string(bytes), "%d", &pid)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pid from %s: %w", path, err)
	}

	path = fmt.Sprintf("%s/%s.%d.ctl", RUN_DIR, binary, pid)
	return Dial("unix", path)
}
