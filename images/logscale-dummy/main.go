package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, "\n")
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
		go http.ListenAndServeTLS(fmt.Sprintf(":%s", esPort), "cert.pem", "key.pem", nil)
	}
	err := http.ListenAndServeTLS(fmt.Sprintf(":%s", humioPort), "cert.pem", "key.pem", nil)
	if err != nil {
		fmt.Printf("got err=%v", err)
	}
}

func runHTTP(humioPort, esPort string) {
	if esPort != "" {
		go http.ListenAndServe(fmt.Sprintf(":%s", esPort), nil)

	}
	err := http.ListenAndServe(fmt.Sprintf(":%s", humioPort), nil)
	if err != nil {
		fmt.Printf("got err=%v", err)
	}
}

/*
 TODO: Consider loading in the "real" certificate from the keystore instead of baking in a cert.pem and key.pem during build.

 TODO: Consider adding functionality that writes a file so "wait for global file in test cases" will pass.
								"ls /mnt/global*.json",
*/
