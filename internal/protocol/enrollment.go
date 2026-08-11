// Package protocol contains SSH request payloads shared by the client and server.
package protocol

import "strings"

const (
	EnrollRequestType = "tearenv-enroll"
	enrollmentPrefix  = "enroll:"
)

type EnrollResponse struct {
	Token string
}

func EnrollmentUser(identity string) string {
	return enrollmentPrefix + identity
}

func EnrollmentIdentity(user string) (string, bool) {
	identity, found := strings.CutPrefix(user, enrollmentPrefix)
	return identity, found && identity != ""
}
