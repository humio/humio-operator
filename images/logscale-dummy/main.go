package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := fmt.Fprintf(w, "\n"); err != nil {
			fmt.Printf("got err=%v", err)
		}
	})

	humioPort := getEnvOrDefault("HUMIO_PORT", "8080")
	esPort := os.Getenv("ELASTIC_PORT")
	tlsEnabled := os.Getenv("TLS_KEYSTORE_LOCATION") != ""

	startServers(humioPort, esPort, tlsEnabled)
}

func startServers(humioPort, esPort string, tlsEnabled bool) {
	if esPort != "" {
		go startServer(esPort, tlsEnabled)
	}
	startServer(humioPort, tlsEnabled)
}

func startServer(port string, tlsEnabled bool) {
	server := &http.Server{
		Addr:              fmt.Sprintf(":%s", port),
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	var err error
	if tlsEnabled {
		fmt.Println("HTTPS")
		err = server.ListenAndServeTLS("cert.pem", "key.pem")
	} else {
		fmt.Println("HTTP")
		err = server.ListenAndServe()
	}

	if err != nil {
		fmt.Printf("got err=%v", err)
	}
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
