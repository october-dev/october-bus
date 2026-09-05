package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

func scopeAdmin(operation string, args []string) error {
	flags := flag.NewFlagSet("scope "+operation, flag.ContinueOnError)
	address := flags.String("address", "", "October Bus address")
	id := flags.String("id", "", "scope ID")
	confirm := flags.String("confirm", "", "confirm the exact scope ID for deletion")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || (operation != "list" && *id == "") {
		return errors.New("scope operation requires --id and no positional arguments")
	}
	if operation == "delete" && (*confirm == "" || *confirm != *id) {
		return errors.New("scope delete requires --confirm matching --id; deletion cannot be undone without a backup")
	}
	client, err := adminClient(*address)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var result any
	switch operation {
	case "list":
		result, err = client.ListScopes(ctx)
	case "rotate-token":
		result, err = client.RotateScopeToken(ctx, *id)
	case "delete":
		err = client.DeleteScope(ctx, *id)
		result = map[string]string{"deletedScopeId": *id}
	default:
		return errors.New("unknown scope admin operation")
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func backupDatabase(args []string) error {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	address := flags.String("address", "", "October Bus address")
	output := flags.String("output", "", "new private SQLite backup path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *output == "" || flags.NArg() != 0 {
		return errors.New("backup requires --output and no positional arguments")
	}
	client, err := adminClient(*address)
	if err != nil {
		return err
	}
	client.HTTP = &http.Client{Timeout: 5 * time.Minute}
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		file.Close()
		if !complete {
			_ = os.Remove(*output)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := client.BackupTo(ctx, file); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	complete = true
	fmt.Printf("Database snapshot saved to %s. It contains credentials; keep it private.\n", *output)
	return nil
}
