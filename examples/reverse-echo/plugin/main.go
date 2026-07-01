package main

import (
	"log"
	"os"
	"slices"

	simplestdioplugin "github.com/concernedmate/simple-stdio-plugin"
)

var plugin *simplestdioplugin.PluginData

func main() {
	router := map[string]func(jsons []byte) ([]byte, error){
		"reverse": func(jsons []byte) ([]byte, error) {
			slices.Reverse(jsons)
			return jsons, nil
		},
	}

	plugin = simplestdioplugin.NewPluginClient(router, os.Stdin, os.Stdout)
	if err := simplestdioplugin.PluginServe(plugin); err != nil {
		log.Fatal(err)
	}
}
