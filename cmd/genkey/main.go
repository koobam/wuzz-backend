// Command genkey prints a fresh Ed25519 keypair in the format the services read:
// base64 of the raw key bytes. Put JWT_PRIVATE_KEY in the user service's secrets
// and JWT_PUBLIC_KEYS everywhere else.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
)

func main() {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("JWT_PRIVATE_KEY=" + base64.StdEncoding.EncodeToString(private))
	fmt.Println("JWT_PUBLIC_KEYS=" + base64.StdEncoding.EncodeToString(public))
}
