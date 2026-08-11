// Package proxy joins two bidirectional byte streams.
package proxy

import "io"

type stream interface {
	io.Reader
	io.Writer
	io.Closer
}

// Join copies data in both directions until both streams finish.
func Join(left, right stream) {
	done := make(chan struct{}, 2)
	copyStream := func(destination, source stream) {
		_, _ = io.Copy(destination, source)
		if writer, ok := destination.(interface{ CloseWrite() error }); ok {
			_ = writer.CloseWrite()
		}
		done <- struct{}{}
	}

	go copyStream(left, right)
	go copyStream(right, left)
	<-done
	<-done
	_ = left.Close()
	_ = right.Close()
}
