package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
)

func main() {
	path := filepath.Join("docs", "data-source-support.md")
	content := database.RenderSupportCatalogMarkdown(database.SupportCatalog(database.NewDriverRegistry()))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s", path)
}
