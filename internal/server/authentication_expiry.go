package server

import (
	"sync"
	"time"

	gliderssh "github.com/gliderlabs/ssh"
	gossh "golang.org/x/crypto/ssh"
)

type authenticationExpiryContextKey struct{}

type authenticationExpiry struct {
	validBefore time.Time
	once        sync.Once
}

func expiringRequestHandler(next gliderssh.RequestHandler) gliderssh.RequestHandler {
	return func(ctx gliderssh.Context, server *gliderssh.Server, request *gossh.Request) (bool, []byte) {
		if !enforceAuthenticationExpiry(ctx) {
			return false, nil
		}
		return next(ctx, server, request)
	}
}

func expiringChannelHandler(next gliderssh.ChannelHandler) gliderssh.ChannelHandler {
	return func(server *gliderssh.Server, connection *gossh.ServerConn, channel gossh.NewChannel, ctx gliderssh.Context) {
		if !enforceAuthenticationExpiry(ctx) {
			_ = channel.Reject(gossh.Prohibited, "authentication expired")
			return
		}
		next(server, connection, channel, ctx)
	}
}

// enforceAuthenticationExpiry starts one connection-level timer on first use.
// Closing the SSH transport also tears down every forwarded service connection.
func enforceAuthenticationExpiry(ctx gliderssh.Context) bool {
	expiry, ok := ctx.Value(authenticationExpiryContextKey{}).(*authenticationExpiry)
	if !ok {
		return true
	}
	connection, ok := ctx.Value(gliderssh.ContextKeyConn).(*gossh.ServerConn)
	if !ok || !time.Now().Before(expiry.validBefore) {
		if ok {
			_ = connection.Close()
		}
		return false
	}
	expiry.once.Do(func() {
		timer := time.AfterFunc(time.Until(expiry.validBefore), func() {
			_ = connection.Close()
		})
		go func() {
			<-ctx.Done()
			timer.Stop()
		}()
	})
	return true
}
