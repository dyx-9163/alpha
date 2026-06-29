package main

import (
	"fmt"
	"log"
	"os"

	"aifar-deployment/backend/internal/config"
	"aifar-deployment/backend/internal/store"
)

func main() {
	cfg := config.Load()
	cmd := "inspect"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	db, err := store.OpenWithSecret(cfg.DatabasePath, cfg.CredentialSecret)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	switch cmd {
	case "inspect":
		inspect(db, cfg.DatabasePath)
	case "reset-admin":
		username := cfg.BootstrapUsername
		password := cfg.BootstrapPassword
		if len(os.Args) > 2 {
			username = os.Args[2]
		}
		if len(os.Args) > 3 {
			password = os.Args[3]
		}
		if err := db.ResetUserPassword(username, password); err != nil {
			log.Fatalf("reset admin: %v", err)
		}
		fmt.Printf("admin credential ready: username=%s password=%s\n", username, password)
	default:
		fmt.Fprintf(os.Stderr, "usage: aifar-admin [inspect|reset-admin [username password]]\n")
		os.Exit(2)
	}
}

func inspect(db *store.Store, path string) {
	fmt.Printf("database: %s\n", path)
	users, err := db.ListUsers()
	if err != nil {
		log.Fatalf("list users: %v", err)
	}
	fmt.Printf("users: %d\n", len(users))
	for _, user := range users {
		fmt.Printf("- %s role=%s created=%s\n", user.Username, user.Role, user.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	for _, table := range []string{"servers", "tasks", "task_logs", "audit_logs", "resources", "app_instances", "settings"} {
		count, err := db.CountRows(table)
		if err != nil {
			log.Fatalf("count %s: %v", table, err)
		}
		fmt.Printf("%s: %d\n", table, count)
	}
}
