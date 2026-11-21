// Command waymark-relay is the Waymark relay for your own server: the last
// way between an Agent and its Controller when they share no network. It
// speaks the same protocol as the Cloudflare Worker in relay/ and sees only
// encrypted data.
//
//	WAYMARK_RELAY_KEY=<access key> waymark-relay -addr :8443 -cert fullchain.pem -key privkey.pem
//
// Without -cert it serves plain HTTP, for use behind a reverse proxy that
// adds TLS (Caddy, nginx). Put the address in the Controller's Settings as
// https://your.host:8443 (or the proxy's address).
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/koulaxizis/waymark/app/internal/relay"
)

func main() {
	addr := flag.String("addr", ":8443", "address to listen on")
	cert := flag.String("cert", "", "TLS certificate file (PEM); empty serves plain HTTP")
	key := flag.String("key", "", "TLS key file (PEM)")
	open := flag.Bool("open", false, "allow running without an access key (anyone may relay through you)")
	flag.Parse()
	access := os.Getenv("WAYMARK_RELAY_KEY")
	if access == "" && !*open {
		log.Fatal("set WAYMARK_RELAY_KEY to an access key, or pass -open to run without one")
	}
	srv := &http.Server{Addr: *addr, Handler: relay.Handler(access), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("Waymark relay on %s (access key %s)", *addr, map[bool]string{true: "set", false: "off"}[access != ""])
	var err error
	if *cert != "" {
		err = srv.ListenAndServeTLS(*cert, *key)
	} else {
		err = srv.ListenAndServe()
	}
	log.Fatal(err)
}
