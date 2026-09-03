package handlers

import (
	"context"
	"net"
)

func PublicTcpHandler(ctx context.Context, conn net.Conn) error {
	// Protocol handling will be installed here in a later episode.
	<-ctx.Done()
	return nil
}
