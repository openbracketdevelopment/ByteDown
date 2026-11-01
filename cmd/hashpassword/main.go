// hashpassword reads a password from stdin; pipe from a hidden shell prompt.
package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	b, e := io.ReadAll(io.LimitReader(os.Stdin, 4096))
	if e != nil {
		panic(e)
	}
	password := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
	if len(password) < 12 {
		fmt.Fprintln(os.Stderr, "Use at least 12 characters")
		os.Exit(1)
	}
	salt := make([]byte, 16)
	if _, e = rand.Read(salt); e != nil {
		panic(e)
	}
	key, e := pbkdf2.Key(sha256.New, password, salt, 600000, 32)
	if e != nil {
		panic(e)
	}
	fmt.Printf("pbkdf2-sha256$600000$%s$%s\n", hex.EncodeToString(salt), hex.EncodeToString(key))
}
