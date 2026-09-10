package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RunStdio cancels active workers immediately when the client disconnects. The
// SDK's normal EOF handling can otherwise wait for active handlers to finish.
func (a *Adapter) RunStdio(ctx context.Context) error {
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopShutdown := context.AfterFunc(ctx, a.stop)
	defer stopShutdown()
	disconnect := func() { a.stop(); cancel() }
	err := a.Server.Run(sessionCtx, &disconnectTransport{Transport: &mcp.StdioTransport{}, cancel: disconnect})
	if ctx.Err() == nil && errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

type disconnectTransport struct {
	mcp.Transport
	cancel context.CancelFunc
}

func (t *disconnectTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	connection, err := t.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &disconnectConnection{Connection: connection, cancel: t.cancel}, nil
}

type disconnectConnection struct {
	mcp.Connection
	cancel context.CancelFunc
}

func (c *disconnectConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	message, err := c.Connection.Read(ctx)
	if err != nil {
		c.cancel()
	}
	return message, err
}
func (c *disconnectConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	err := c.Connection.Write(ctx, message)
	if err != nil {
		c.cancel()
	}
	return err
}
func (c *disconnectConnection) Close() error { c.cancel(); return c.Connection.Close() }
