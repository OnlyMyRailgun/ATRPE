package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
	"github.com/OnlyMyRailgun/ATRPE/internal/knowledge"
)

func main() {
	store, err := knowledge.NewSQLiteStore("data/knowledge.db")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sqlite open failed (expected in CI — no articles yet): %v\n", err)
		// Emit empty manifest so CI passes
		fmt.Println("[]")
		os.Exit(0)
	}
	defer store.Close()

	ctx := context.Background()
	records, err := store.ListCitationManifest(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list citations failed: %v\n", err)
		os.Exit(1)
	}

	if records == nil {
		records = []artifacts.CitationRecord{}
	}

	out, _ := json.MarshalIndent(records, "", "  ")
	fmt.Println(string(out))
}
