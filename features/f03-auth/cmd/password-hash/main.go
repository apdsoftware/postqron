package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	auth "github.com/apdsoftware/postqron/features/f03-auth"
	"golang.org/x/term"
)

func main() {
	base64Output := flag.Bool(
		"base64",
		false,
		"encode the PHC value for POSTQRON_ADMIN_PASSWORD_HASH_B64",
	)
	passwordStdin := flag.Bool(
		"password-stdin",
		false,
		"read the password from standard input without a terminal prompt",
	)
	flag.Parse()
	var password []byte
	var err error
	if *passwordStdin {
		password, err = io.ReadAll(io.LimitReader(os.Stdin, 1025))
		password = []byte(strings.TrimSuffix(
			strings.TrimSuffix(string(password), "\n"),
			"\r",
		))
	} else {
		fmt.Fprint(os.Stderr, "Password: ")
		password, err = term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "read password:", err)
		os.Exit(1)
	}
	value := strings.TrimSuffix(string(password), "\r")
	hash, err := auth.HashPassword(value, auth.DefaultPasswordParameters())
	for index := range password {
		password[index] = 0
	}
	value = ""
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash password:", err)
		os.Exit(1)
	}
	if *base64Output {
		hash = base64.StdEncoding.EncodeToString([]byte(hash))
	}
	fmt.Println(hash)
}
