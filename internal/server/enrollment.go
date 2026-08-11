package server

import (
	"log/slog"

	"github.com/fr0stylo/tearenv/internal/protocol"
	gliderssh "github.com/gliderlabs/ssh"
	"golang.org/x/crypto/ssh"
)

type enrollmentContextKey struct{}

type enrollmentAttempt struct {
	Identity string
	Invite   string
}

func enrollmentHandler(credentials *Credentials, logger *slog.Logger) gliderssh.RequestHandler {
	return func(ctx gliderssh.Context, _ *gliderssh.Server, _ *ssh.Request) (bool, []byte) {
		attempt, ok := ctx.Value(enrollmentContextKey{}).(enrollmentAttempt)
		if !ok {
			return false, nil
		}
		token, err := credentials.Enroll(attempt.Identity, attempt.Invite)
		if err != nil {
			logger.Warn("client enrollment failed", "identity", attempt.Identity, "error", err)
			return false, nil
		}
		logger.Info("client enrolled", "identity", attempt.Identity, "remote", ctx.RemoteAddr())
		return true, ssh.Marshal(protocol.EnrollResponse{Token: token})
	}
}
