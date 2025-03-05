package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		_, err := fmt.Fprintf(w, "\n")
		fmt.Printf("got err=%v", err)
	})

	humioPort := os.Getenv("HUMIO_PORT")
	esPort := os.Getenv("ELASTIC_PORT")
	_, tlsEnabled := os.LookupEnv("TLS_KEYSTORE_LOCATION")

	if humioPort != "" {
		humioPort = "8080"
	}

	if tlsEnabled {
		fmt.Println("HTTPS")
		runHTTPS(humioPort, esPort)
	} else {
		fmt.Println("HTTP")
		runHTTP(humioPort, esPort)
	}
}

func runHTTPS(humioPort, esPort string) {
	if esPort != "" {
		go func() {
			err := http.ListenAndServeTLS(fmt.Sprintf(":%s", esPort), "cert.pem", "key.pem", nil)
			fmt.Printf("got err=%v", err)
		}()
	}
	err := http.ListenAndServeTLS(fmt.Sprintf(":%s", humioPort), "cert.pem", "key.pem", nil)
	if err != nil {
		fmt.Printf("got err=%v", err)
	}
}

func runHTTP(humioPort, esPort string) {
	if esPort != "" {
		go func() {
			err := http.ListenAndServe(fmt.Sprintf(":%s", esPort), nil)
			fmt.Printf("got err=%v", err)
		}()

	}
	err := http.ListenAndServe(fmt.Sprintf(":%s", humioPort), nil)
	if err != nil {
		fmt.Printf("got err=%v", err)
	}
}
