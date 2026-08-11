package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	message := os.Getenv("TEARENV_HTTP_RESPONSE")
	if message == "" {
		message = "tearenv kind scaling works"
	}
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		response.Header().Set("Connection", "close")
		_, _ = fmt.Fprintln(response, message)
	})
	server := &http.Server{
		Addr:              ":8080",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}
