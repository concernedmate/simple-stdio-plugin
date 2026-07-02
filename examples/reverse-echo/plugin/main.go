package main

import (
	"log"
	"os"
	"slices"

	simplestdioplugin "github.com/concernedmate/simple-stdio-plugin"
)

var plugin *simplestdioplugin.PluginData

func main() {
	router := map[string]func(data []byte) ([]byte, error){
		"reverse": func(data []byte) ([]byte, error) {
			slices.Reverse(data)
			return data, nil
		},
	}

	plugin = simplestdioplugin.NewPluginClient(router, os.Stdin, os.Stdout)
	if err := simplestdioplugin.PluginServe(plugin); err != nil {
		log.Fatal(err)
	}
}
