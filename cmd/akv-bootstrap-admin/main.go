package main

import (
	"bytes"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"

	"github.com/fallingnight/akv/internal/identity"
	"github.com/fallingnight/akv/internal/proxy"
	"github.com/fallingnight/akv/internal/store"
	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/term"
)

func main() {
	username := flag.String("username", "", "initial administrator username")
	flag.Parse()
	if *username == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: akv-bootstrap-admin -username NAME")
		os.Exit(2)
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, "password input requires an interactive terminal")
		os.Exit(2)
	}
	password := readPassword("Password: ")
	confirmation := readPassword("Confirm password: ")
	defer clear(password)
	defer clear(confirmation)
	if len(password) == 0 || !bytes.Equal(password, confirmation) {
		fmt.Fprintln(os.Stderr, "passwords do not match or are empty")
		os.Exit(2)
	}

	dsn, err := proxy.ReadProtectedConfigFile(os.Getenv("AKV_DATABASE_DSN_FILE"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid database configuration")
		os.Exit(2)
	}
	database, err := sql.Open("pgx", string(dsn))
	clear(dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "database unavailable")
		os.Exit(1)
	}
	defer database.Close()
	ctx := context.Background()
	if err := database.PingContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "database unavailable")
		os.Exit(1)
	}
	if err := store.Migrate(ctx, store.NewPostgreSQLMigrationStore(database)); err != nil {
		fmt.Fprintln(os.Stderr, "database migration failed")
		os.Exit(1)
	}
	service, err := identity.NewService(store.NewPostgreSQLIdentityRepository(database))
	if err != nil {
		fmt.Fprintln(os.Stderr, "identity initialization failed")
		os.Exit(1)
	}
	if _, err := service.BootstrapAdmin(ctx, *username, string(password)); err != nil {
		fmt.Fprintln(os.Stderr, "administrator bootstrap failed")
		os.Exit(1)
	}
	clear(password)
	fmt.Println("initial administrator created")
}

func readPassword(prompt string) []byte {
	fmt.Fprint(os.Stderr, prompt)
	value, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "password input failed")
		os.Exit(2)
	}
	return value
}

func clear(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
