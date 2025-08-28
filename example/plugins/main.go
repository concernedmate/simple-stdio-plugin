package main

import (
	"log"
	"os"

	simplestdioplugin "github.com/concernedmate/simple-stdio-plugin"
)

func main() {
	router := map[string]func(jsons []byte) ([]byte, error){
		"tes-command": func(jsons []byte) ([]byte, error) {
			return []byte("testing"), nil
		},
	}
	plugin := simplestdioplugin.NewPlugin(router, os.Stdin, os.Stdout)
	if err := simplestdioplugin.PluginServe(plugin); err != nil {
		log.Fatal(err)
	}
}
